// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

type contextKey string

const ClientIPContextKey contextKey = "ClientIP"

type ClientIPMiddleware struct {
	extractors map[ExtractorType]ClientIPExtractor
	priority   []ExtractorType
	metrics    *metrics
	logger     log.Logger
}

func New(cfg Config, reg prometheus.Registerer, logger log.Logger) (*ClientIPMiddleware, error) {
	mw := ClientIPMiddleware{
		extractors: make(map[ExtractorType]ClientIPExtractor, 0),
		priority:   make([]ExtractorType, 0),
		logger:     logger,
	}
	for t := range cfg.Type {
		switch t {
		case ExtractorTypeRemoteAddr:
			mw.extractors[ExtractorTypeRemoteAddr] = RemoteAddrClientIPExtractor{}
		case ExtractorTypeXFF:
			mw.extractors[ExtractorTypeXFF] = NewXFFClientIPExtractor(cfg.TrustedProxyCount)
		case ExtractorTypeXRealIP:
			mw.extractors[ExtractorTypeXRealIP] = XRealIPClientIPExtractor{}
		default:
			return nil, fmt.Errorf("failed to initialize ClientIPMiddleware")
		}
	}
	for _, t := range ExtractorTypePriority {
		if _, ok := mw.extractors[t]; ok {
			mw.priority = append(mw.priority, t)
		}
	}
	level.Debug(logger).Log("msg", "client-ip-middleware extractors", "priority", fmt.Sprintf("%+v", mw.priority))
	mw.metrics = newMetrics(reg)
	return &mw, nil
}

// GetClientIP executes each extractor based on priority order and returns the first result that is not an error
func (a *ClientIPMiddleware) GetClientIP(r *http.Request) (string, error) {
	var ip string
	var lastError error
	for _, t := range a.priority {
		if e, ok := a.extractors[t]; ok {
			ip, lastError = e.GetClientIP(r)
			if lastError == nil {
				level.Debug(a.logger).Log("msg", "client-ip-middleware IP found", "ip", ip, "extractor", t)
				return ip, lastError
			}
		}
	}
	return "", lastError
}

func (a *ClientIPMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if ip, err := a.GetClientIP(r); err == nil {
			ctx = InjectClientIP(ctx, net.ParseIP(ip).String())
		} else {
			a.metrics.failures.WithLabelValues(errorToLabel(err)).Inc()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ExtractClientIP(ctx context.Context) string {
	clientIP := ctx.Value(ClientIPContextKey)
	if clientIP == nil {
		return ""
	}
	return clientIP.(string)
}

func InjectClientIP(ctx context.Context, clientIP string) context.Context {
	return context.WithValue(ctx, ClientIPContextKey, clientIP)
}

func errorToLabel(err error) string {
	switch err {
	case ErrXRealIPAddrEmpty:
		return "xrealip_empty"
	case ErrXRealIPAddrInvalid:
		return "xrealip_invalid_ip"
	case ErrXFFMissing:
		return "xff_missing"
	case ErrXFFTooShort:
		return "xff_too_short"
	case ErrXFFInvalidIP:
		return "xff_invalid_ip"
	case ErrRemoteAddrEmpty:
		return "remoteaddr_empty"
	case ErrRemoteAddrInvalid:
		return "remoteaddr_invalid"
	}

	return "unknown"
}
