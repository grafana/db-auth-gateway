// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	common_middleware "github.com/grafana/dskit/middleware"
)

type OverridesMiddleware struct {
	cfg         *RuntimeConfigTenantOverrides
	middlewares []common_middleware.Interface
}

func NewOverridesMiddleware(cfg *RuntimeConfigTenantOverrides) common_middleware.Interface {
	om := OverridesMiddleware{cfg: cfg}
	// Create instances of all backend specific overrides middlewares here.
	om.middlewares = append(om.middlewares, NewLokiOverridesMiddleware(cfg))
	om.middlewares = append(om.middlewares, NewMimirOverridesMiddleware(cfg))

	return &om
}

func (om *OverridesMiddleware) Wrap(next http.Handler) http.Handler {
	for _, mw := range om.middlewares {
		next = mw.Wrap(next)
	}
	return next
}

func writeError(_ context.Context, w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	b, _ := json.Marshal(struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}{Status: "error", Error: msg})
	_, _ = w.Write(b)
}
