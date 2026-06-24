// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteAddrClientIPExtractor_GetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
		wantErr    error
	}{
		{
			name:       "RemoteAddr IPv4 without port",
			remoteAddr: "192.168.10.1",
			want:       "192.168.10.1",
			wantErr:    nil,
		},
		{
			name:       "RemoteAddr IPv4 with port",
			remoteAddr: "192.168.10.1:50123",
			want:       "192.168.10.1",
			wantErr:    nil,
		},
		{
			name:       "RemoteAddr invalid IPv4",
			remoteAddr: "192.168.10.1000",
			want:       "",
			wantErr:    ErrRemoteAddrClientIPExtractor,
		},
		{
			name:       "RemoteAddr IPv6",
			remoteAddr: "2001:db8::ff00:42:8329",
			want:       "2001:db8::ff00:42:8329",
			wantErr:    nil,
		},
		{
			name:       "RemoteAddr IPv6 with port",
			remoteAddr: "[2001:db8::ff00:42:8329]:1024",
			want:       "2001:db8::ff00:42:8329",
			wantErr:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &RemoteAddrClientIPExtractor{}

			req := &http.Request{RemoteAddr: tt.remoteAddr}

			ip, err := c.GetClientIP(req)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, ip)
		})
	}
}
