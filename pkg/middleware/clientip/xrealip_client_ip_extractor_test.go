// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestXRealIPClientIPExtractor_GetClientIP(t *testing.T) {
	tests := []struct {
		name        string
		xRealIPAddr string
		want        string
		wantErr     error
	}{
		{
			name:        "X-Real-IP IPv4",
			xRealIPAddr: "1.2.3.4",
			want:        "1.2.3.4",
			wantErr:     nil,
		},
		{
			name:        "X-Real-IP IPv4 with port",
			xRealIPAddr: "1.2.3.4:80",
			want:        "1.2.3.4",
			wantErr:     nil,
		},
		{
			name:        "X-Real-IP IPv6",
			xRealIPAddr: "2001:db8::ff00:42:8329",
			want:        "2001:db8::ff00:42:8329",
			wantErr:     nil,
		},
		{
			name:        "X-Real-IP empty",
			xRealIPAddr: "",
			want:        "",
			wantErr:     ErrXRealIPClientIPExtractor,
		},
		{
			name:        "X-Real-IP invalid",
			xRealIPAddr: "not-an-ip",
			want:        "",
			wantErr:     ErrXRealIPClientIPExtractor,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := XRealIPClientIPExtractor{}

			req := &http.Request{Header: map[string][]string{}}
			if tt.xRealIPAddr != "" {
				req.Header.Set(XRealIPHeader, tt.xRealIPAddr)
			}

			ip, err := e.GetClientIP(req)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, ip)
		})
	}
}
