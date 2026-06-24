// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestXFFClientIPExtractor_GetClientIP(t *testing.T) {
	tests := []struct {
		name                 string
		xffTrustedProxyCount int
		xffHeaders           []string
		want                 string
		wantErr              error
	}{
		{
			name:                 "Client IP first in XFF",
			xffTrustedProxyCount: 2,
			xffHeaders:           []string{"1.1.1.1, 2.2.2.2, 3.3.3.3"},
			want:                 "1.1.1.1",
			wantErr:              nil,
		},
		{
			name:                 "Client IP middle in XFF",
			xffTrustedProxyCount: 1,
			xffHeaders:           []string{"1.1.1.1, 2.2.2.2, 3.3.3.3"},
			want:                 "2.2.2.2",
			wantErr:              nil,
		},
		{
			name:                 "Client IP last in XFF",
			xffTrustedProxyCount: 0,
			xffHeaders:           []string{"1.1.1.1, 2.2.2.2, 3.3.3.3"},
			want:                 "3.3.3.3",
			wantErr:              nil,
		},
		{
			name:                 "XFF too short",
			xffTrustedProxyCount: 5,
			xffHeaders:           []string{"1.1.1.1, 2.2.2.2, 3.3.3.3"},
			want:                 "",
			wantErr:              ErrXFFClientIPExtractor,
		},
		{
			name:                 "Multiple XFF headers",
			xffTrustedProxyCount: 3,
			xffHeaders: []string{
				"1.1.1.1, 2.2.2.2, 3.3.3.3",
				"4.4.4.4, 5.5.5.5",
			},
			want:    "2.2.2.2",
			wantErr: nil,
		},
		{
			name:                 "Parsing XFF IP formats/all acceptable",
			xffTrustedProxyCount: 6,
			xffHeaders: []string{
				// IPv4 w/ extra spaces, IPv4 w/ port
				" 1.1.1.1  , 2.2.2.2:80",
				// Same, w/ square brackets
				"[3.3.3.3], [4.4.4.4]:80",
				// IPv6, IPv6 w/ port, IPv6 in square brackets w/o port
				"2001:db8::ff00:42:8329, [2345:0425:2CA1::0567:5673:23b5]:80, [2345:0425:2CA1::0567:5673:23b5]",
			},
			want:    "1.1.1.1",
			wantErr: nil,
		},
		{
			name:                 "Parsing XFF IP formats/unparseable IP",
			xffTrustedProxyCount: 1,
			xffHeaders:           []string{"1.1.1.1, 2.2.2.2::80"},
			want:                 "",
			wantErr:              ErrXFFClientIPExtractor,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &XFFClientIPExtractor{
				xffTrustedProxyCount: tt.xffTrustedProxyCount,
			}

			req := &http.Request{Header: map[string][]string{}}
			for _, h := range tt.xffHeaders {
				req.Header.Add(XForwardedForHeader, h)
			}

			ip, err := c.GetClientIP(req)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, ip)
		})
	}
}
