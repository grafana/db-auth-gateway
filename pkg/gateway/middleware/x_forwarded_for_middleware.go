// SPDX-License-Identifier: AGPL-3.0-only

package middleware

import (
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"github.com/grafana/db-auth-gateway/pkg/middleware/clientip"
)

// XForwardedForMiddleware is a middleware that sets the X-Forwarded-For header
// if it's not already set, using the client IP from the context.
// It depends on clientIPMiddleware having run first to populate the context.
type XForwardedForMiddleware struct {
	logger log.Logger
}

// NewXForwardedForMiddleware creates a new XForwardedForMiddleware.
func NewXForwardedForMiddleware(logger log.Logger) *XForwardedForMiddleware {
	return &XForwardedForMiddleware{
		logger: logger,
	}
}

// Wrap implements the middleware.Interface interface.
func (m *XForwardedForMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only set X-Forwarded-For if it's not already set.
		if r.Header.Get(clientip.XForwardedForHeader) == "" {
			ip := clientip.ExtractClientIP(r.Context())
			if ip != "" {
				r.Header.Set(clientip.XForwardedForHeader, ip)
			} else {
				level.Debug(m.logger).Log("msg", "clientIP from context was empty, not setting X-Forwarded-For header")
			}
		}
		next.ServeHTTP(w, r)
	})
}
