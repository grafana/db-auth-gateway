// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const XForwardedForHeader = "X-Forwarded-For"

var (
	ErrXFFClientIPExtractor = errors.New("XFFClientIPExtractor error")
	ErrXFFMissing           = fmt.Errorf("%w: X-Forwarded-For header missing", ErrXFFClientIPExtractor)
	ErrXFFTooShort          = fmt.Errorf("%w: X-Forwarded-For header too short", ErrXFFClientIPExtractor)
	ErrXFFInvalidIP         = fmt.Errorf("%w: X-Forwarded-For header contains invalid IP", ErrXFFClientIPExtractor)
)

type XFFClientIPExtractor struct {
	xffTrustedProxyCount int
}

func NewXFFClientIPExtractor(XFFTrustedProxyCount int) *XFFClientIPExtractor {
	return &XFFClientIPExtractor{xffTrustedProxyCount: XFFTrustedProxyCount}
}

func (c *XFFClientIPExtractor) GetClientIP(r *http.Request) (string, error) {
	// Multiple XFF headers can be set. If so, they have to be treated as a single
	// list of IPs, in order of appearance.
	xffs := r.Header.Values(XForwardedForHeader)
	if len(xffs) == 0 {
		return "", ErrXFFMissing
	}

	ips := make([]string, 0)
	for _, xff := range xffs {
		ips = append(ips, strings.Split(xff, ",")...)
	}

	if c.xffTrustedProxyCount < 0 || c.xffTrustedProxyCount > len(ips)-1 {
		return "", ErrXFFTooShort
	}

	// Search for position xffTrustedProxyCount from the end, and validate each IP along the way.
	// The IPs to the left of the position we're looking for are ignored.
	var ip string
	ipIdx := len(ips) - c.xffTrustedProxyCount - 1
	for i := len(ips) - 1; i >= ipIdx; i-- {
		ip = strings.TrimSpace(ips[i])
		// Extract the host part of a host:port representation.
		// This covers both IPv4 and IPv6, including the case where `[]` are used.
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		} else if len(ip) > 0 && ip[0] == '[' && ip[len(ip)-1] == ']' {
			// This is a corner case, probably invalid, but it may be encountered:
			// a single IP may be surrounded by [], for example [192.168.10.1].
			ip = strings.Trim(ip, "[]")
		}

		if net.ParseIP(ip) == nil {
			return "", ErrXFFInvalidIP
		}
	}

	return ip, nil
}
