// SPDX-License-Identifier: AGPL-3.0-only

package middleware

import (
	"net/http"

	"github.com/grafana/dskit/middleware"
	"github.com/grafana/dskit/tracing"
)

type propagateTraceIDMiddleware struct {
}

// NewPropagateTraceIDMiddleware returns a middleware that propagates the trace ID from request context into the response headers.
// This is useful for correlating user facing errors messages with the corresponding distributred trace.
func NewPropagateTraceIDMiddleware() middleware.Interface {
	return &propagateTraceIDMiddleware{}
}

// Wrap implements middleware.Interface.
func (m *propagateTraceIDMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if traceID, ok := tracing.ExtractTraceID(r.Context()); ok {
			w.Header().Set("grafana-trace-id", traceID)
		}
		next.ServeHTTP(w, r)
	})
}
