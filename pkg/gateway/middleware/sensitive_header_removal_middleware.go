// SPDX-License-Identifier: AGPL-3.0-only

package middleware

import (
	"net/http"

	"github.com/grafana/dskit/middleware"
)

var defaultHeaders = []string{
	"X-Access-Token",
	"X-Grafana-Id",
}

type sensitiveHeaderRemovalMiddleware struct {
	headersToRemove map[string]struct{}
}

func NewSensitiveHeaderRemovalMiddleware(headers []string) middleware.Interface {
	headersToRemove := make(map[string]struct{}, len(headers)+len(defaultHeaders))

	for _, h := range headers {
		headersToRemove[h] = struct{}{}
	}

	for _, h := range defaultHeaders {
		headersToRemove[h] = struct{}{}
	}

	return &sensitiveHeaderRemovalMiddleware{
		headersToRemove: headersToRemove,
	}
}

func (h *sensitiveHeaderRemovalMiddleware) Wrap(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for h := range h.headersToRemove {
			r.Header.Del(h)
		}

		handler.ServeHTTP(w, r)
	})
}
