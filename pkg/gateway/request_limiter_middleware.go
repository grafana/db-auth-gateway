// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"net/http"
	"sync/atomic"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/middleware"
)

type requestLimiterMiddleware struct {
	limit            int32
	inFlightRequests int32
	logger           log.Logger
}

// newRequestLimiterMiddleware returns an HTTP middleware that restricts the maximum amount of in-flight HTTP requests.
// If lm value is <= 0, no limit is assumed.
func newRequestLimiterMiddleware(lm int, logger log.Logger) middleware.Interface {
	return &requestLimiterMiddleware{
		limit:  int32(lm),
		logger: logger,
	}
}

// Wrap implements middleware.Interface.
func (m *requestLimiterMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.limit <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		activeReqCount := atomic.AddInt32(&m.inFlightRequests, 1)
		defer atomic.AddInt32(&m.inFlightRequests, -1)

		if activeReqCount > m.limit {
			level.Warn(m.logger).Log("msg", "reached maximum amount of HTTP requests", "limit", m.limit)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
