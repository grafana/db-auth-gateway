// SPDX-License-Identifier: AGPL-3.0-only

package healthcheck

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/services"
	"github.com/prometheus/client_golang/prometheus"
)

// Checker periodically checks the health of configured endpoints and reports
// their status via Prometheus metrics.
type Checker struct {
	services.Service

	endpoints []Endpoint
	metrics   *Metrics
	logger    log.Logger

	// httpClient is used for healthcheck requests.
	httpClient *http.Client

	// mu protects failureCounts, healthStatus, and lastChecked.
	mu            sync.RWMutex
	failureCounts map[string]int
	healthStatus  map[string]bool
	lastChecked   map[string]time.Time
}

// NewChecker creates a new healthcheck Checker.
func NewChecker(
	endpoints []Endpoint,
	reg prometheus.Registerer,
	namespace string,
	logger log.Logger,
) (*Checker, error) {
	componentLogger := log.With(logger, "component", "healthcheck")

	// Filter to only enabled endpoints with valid URLs and positive intervals.
	var enabledEndpoints []Endpoint
	for _, ep := range endpoints {
		if !ep.Config.Enabled || ep.URL == "" {
			continue
		}
		// Skip endpoints with non-positive intervals to prevent panics.
		if ep.Config.Interval <= 0 {
			level.Warn(componentLogger).Log(
				"msg", "skipping endpoint with non-positive healthcheck interval",
				"endpoint", ep.Name,
				"interval", ep.Config.Interval,
			)
			continue
		}
		enabledEndpoints = append(enabledEndpoints, ep)
	}

	if len(enabledEndpoints) == 0 {
		// Return a no-op service if no healthchecks are configured.
		return nil, nil
	}

	metrics := NewMetrics(reg, namespace)

	c := &Checker{
		endpoints:     enabledEndpoints,
		metrics:       metrics,
		logger:        componentLogger,
		failureCounts: make(map[string]int),
		healthStatus:  make(map[string]bool),
		lastChecked:   make(map[string]time.Time),
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
			// Don't set a global timeout here; we use per-request timeouts.
		},
	}

	// Initialize internal state; do not set Health metric until first pass/fail.
	for _, ep := range enabledEndpoints {
		c.healthStatus[ep.Name] = true
	}

	// Find the minimum interval to use as the tick interval.
	minInterval := findMinInterval(enabledEndpoints)

	c.Service = services.NewTimerService(minInterval, c.starting, c.iteration, c.stopping)
	return c, nil
}

func findMinInterval(endpoints []Endpoint) time.Duration {
	minInterval := time.Hour
	for _, ep := range endpoints {
		if ep.Config.Interval < minInterval {
			minInterval = ep.Config.Interval
		}
	}
	return minInterval
}

func (c *Checker) starting(_ context.Context) error {
	level.Info(c.logger).Log("msg", "starting healthcheck service", "endpoints", len(c.endpoints))
	return nil
}

func (c *Checker) stopping(_ error) error {
	level.Info(c.logger).Log("msg", "stopping healthcheck service")
	return nil
}

func (c *Checker) iteration(ctx context.Context) error {
	now := time.Now()
	var wg sync.WaitGroup

	for _, ep := range c.endpoints {
		// Check if this endpoint's interval has elapsed since last check.
		c.mu.RLock()
		lastCheck := c.lastChecked[ep.Name]
		c.mu.RUnlock()

		if now.Sub(lastCheck) < ep.Config.Interval {
			// Not time to check this endpoint yet.
			continue
		}

		// Update last checked time before starting the check.
		c.mu.Lock()
		c.lastChecked[ep.Name] = now
		c.mu.Unlock()

		wg.Add(1)
		go func(ep Endpoint) {
			defer wg.Done()
			c.checkEndpoint(ctx, ep)
		}(ep)
	}
	wg.Wait()
	return nil
}

func (c *Checker) checkEndpoint(ctx context.Context, ep Endpoint) {
	start := time.Now()

	// Build the healthcheck URL.
	healthURL, err := buildHealthcheckURL(ep.URL, ep.Config.Path, ep.Config.Port)
	if err != nil {
		level.Error(c.logger).Log(
			"msg", "failed to build healthcheck URL",
			"endpoint", ep.Name,
			"url", ep.URL,
			"path", ep.Config.Path,
			"err", err,
		)
		c.recordFailure(ep, ResultError)
		return
	}

	// Create request with timeout.
	reqCtx, cancel := context.WithTimeout(ctx, ep.Config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		level.Error(c.logger).Log(
			"msg", "failed to create healthcheck request",
			"endpoint", ep.Name,
			"err", err,
		)
		c.recordFailure(ep, ResultError)
		return
	}

	resp, err := c.httpClient.Do(req)
	duration := time.Since(start)
	c.metrics.CheckDuration.WithLabelValues(ep.Name).Observe(duration.Seconds())

	if err != nil {
		if ctx.Err() != nil {
			// Context cancelled, likely shutting down.
			return
		}
		result := ResultError
		if isTimeout(err) {
			result = ResultTimeout
		}
		level.Warn(c.logger).Log(
			"msg", "healthcheck request failed",
			"endpoint", ep.Name,
			"url", healthURL,
			"err", err,
			"duration", duration,
		)
		c.recordFailure(ep, result)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.recordSuccess(ep)
	} else {
		level.Warn(c.logger).Log(
			"msg", "healthcheck returned non-2xx status",
			"endpoint", ep.Name,
			"url", healthURL,
			"status", resp.StatusCode,
			"duration", duration,
		)
		c.recordFailure(ep, ResultFailure)
	}
}

func (c *Checker) recordSuccess(ep Endpoint) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Reset failure count on success.
	c.failureCounts[ep.Name] = 0

	// Mark as healthy if not already.
	if !c.healthStatus[ep.Name] {
		level.Info(c.logger).Log(
			"msg", "endpoint recovered",
			"endpoint", ep.Name,
			"url", ep.URL,
		)
		c.healthStatus[ep.Name] = true
	}
	c.metrics.Health.WithLabelValues(ep.Name).Set(1)
	c.metrics.ChecksTotal.WithLabelValues(ep.Name, ResultSuccess).Inc()
}

func (c *Checker) recordFailure(ep Endpoint, result string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failureCounts[ep.Name]++
	c.metrics.ChecksTotal.WithLabelValues(ep.Name, result).Inc()

	// Only mark unhealthy after reaching retry threshold.
	if c.failureCounts[ep.Name] > ep.Config.Retries {
		if c.healthStatus[ep.Name] {
			level.Error(c.logger).Log(
				"msg", "endpoint marked unhealthy",
				"endpoint", ep.Name,
				"url", ep.URL,
				"consecutive_failures", c.failureCounts[ep.Name],
			)
			c.healthStatus[ep.Name] = false
			c.metrics.Health.WithLabelValues(ep.Name).Set(0)
		}
	}
}

// IsHealthy returns the current health status of an endpoint.
func (c *Checker) IsHealthy(endpointName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	healthy, ok := c.healthStatus[endpointName]
	return !ok || healthy // If not tracked, assume healthy.
}

// buildHealthcheckURL constructs the full healthcheck URL from the base URL, path, and optional port.
// If port is positive, it overrides the port in the base URL.
func buildHealthcheckURL(baseURL, path string, port int) (string, error) {
	// Replace non-HTTP schemes with http for healthcheck requests.
	// Order matters: longer prefixes first (e.g. dns:/// before dns://).
	cleanURL := baseURL
	if !strings.HasPrefix(cleanURL, "http://") && !strings.HasPrefix(cleanURL, "https://") {
		for _, prefix := range []string{"dns:///", "grpc-proxy://", "kubernetes://", "dns://", "h2c://", "grpc:"} {
			if strings.HasPrefix(cleanURL, prefix) {
				cleanURL = "http://" + strings.TrimPrefix(cleanURL, prefix)
				break
			}
		}
		if cleanURL == baseURL {
			cleanURL = "http://" + cleanURL // no scheme
		}
	}

	parsed, err := url.Parse(cleanURL)
	if err != nil {
		return "", err
	}

	if port > 0 {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port))
	}

	// Ensure path starts with /.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String(), nil
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	// Check for context deadline exceeded.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Check for net.Error timeout.
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return false
}
