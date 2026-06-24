// SPDX-License-Identifier: AGPL-3.0-only

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/assert"

	"github.com/grafana/db-auth-gateway/pkg/middleware/clientip"
)

func TestXForwardedForMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		clientIP       string
		existingHeader string
		expectedHeader string
	}{
		{
			name:           "sets X-Forwarded-For when not present",
			clientIP:       "1.2.3.4",
			existingHeader: "",
			expectedHeader: "1.2.3.4",
		},
		{
			name:           "does not override existing X-Forwarded-For",
			clientIP:       "1.2.3.4",
			existingHeader: "5.6.7.8",
			expectedHeader: "5.6.7.8",
		},
		{
			name:           "does not set X-Forwarded-For when client IP is empty",
			clientIP:       "",
			existingHeader: "",
			expectedHeader: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			mw := NewXForwardedForMiddleware(log.NewNopLogger())

			req := httptest.NewRequest("GET", "/", nil)
			if tt.existingHeader != "" {
				req.Header.Set(clientip.XForwardedForHeader, tt.existingHeader)
			}
			if tt.clientIP != "" {
				ctx := clientip.InjectClientIP(req.Context(), tt.clientIP)
				req = req.WithContext(ctx)
			}

			rr := httptest.NewRecorder()
			mw.Wrap(handler).ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tt.expectedHeader, req.Header.Get(clientip.XForwardedForHeader))
		})
	}
}
