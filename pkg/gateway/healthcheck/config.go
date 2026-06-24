// SPDX-License-Identifier: AGPL-3.0-only

package healthcheck

import (
	"flag"
	"time"
)

// EndpointConfig holds healthcheck configuration for a single endpoint.
type EndpointConfig struct {
	// Enabled enables healthchecking for this endpoint.
	Enabled bool `yaml:"enabled"`

	// Path is the healthcheck path to call (e.g., "/ready", "/health").
	Path string `yaml:"path"`

	// Timeout is the maximum time to wait for a healthcheck response.
	Timeout time.Duration `yaml:"timeout"`

	// Interval is how often to perform the healthcheck.
	Interval time.Duration `yaml:"interval"`

	// Retries is the number of consecutive failures before marking unhealthy.
	Retries int `yaml:"retries"`

	// Port overrides the port in the endpoint URL for the healthcheck request (0 = use URL as-is).
	Port int `yaml:"port"`
}

// RegisterFlagsWithPrefix registers the healthcheck flags with the given prefix.
// For example, if prefix is "mimir.aggregations", flags will be:
// - mimir.aggregations.healthcheck.enabled
// - mimir.aggregations.healthcheck.path
// - mimir.aggregations.healthcheck.timeout
// - mimir.aggregations.healthcheck.interval
// - mimir.aggregations.healthcheck.retries
// - mimir.aggregations.healthcheck.port
func (cfg *EndpointConfig) RegisterFlagsWithPrefix(prefix string, f *flag.FlagSet) {
	f.BoolVar(&cfg.Enabled, prefix+".healthcheck.enabled", false, "enable healthchecking for this endpoint")
	f.StringVar(&cfg.Path, prefix+".healthcheck.path", "/ready", "healthcheck path to call")
	f.DurationVar(&cfg.Timeout, prefix+".healthcheck.timeout", 5*time.Second, "healthcheck request timeout")
	f.DurationVar(&cfg.Interval, prefix+".healthcheck.interval", 30*time.Second, "how often to perform the healthcheck")
	f.IntVar(&cfg.Retries, prefix+".healthcheck.retries", 2, "number of consecutive failures before marking unhealthy")
	f.IntVar(&cfg.Port, prefix+".healthcheck.port", 0, "port to use for healthcheck requests (0 = use endpoint URL port)")
}

// Endpoint represents a configured endpoint with its healthcheck settings.
type Endpoint struct {
	// Name is a human-readable identifier for the endpoint (e.g., "aggregations").
	Name string

	// URL is the base URL of the endpoint.
	URL string

	// Config holds the healthcheck configuration.
	Config EndpointConfig
}

// EndpointBuilder helps collect endpoints and their healthcheck configurations.
type EndpointBuilder struct {
	endpoints []Endpoint
}

// NewEndpointBuilder creates a new EndpointBuilder.
func NewEndpointBuilder() *EndpointBuilder {
	return &EndpointBuilder{}
}

// Add adds an endpoint to the builder.
func (b *EndpointBuilder) Add(name, url string, cfg EndpointConfig) *EndpointBuilder {
	b.endpoints = append(b.endpoints, Endpoint{
		Name:   name,
		URL:    url,
		Config: cfg,
	})
	return b
}

// Build returns the collected endpoints.
func (b *EndpointBuilder) Build() []Endpoint {
	return b.endpoints
}
