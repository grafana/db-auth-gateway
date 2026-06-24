// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

const XRealIPHeader = "X-Real-IP"

var (
	ErrXRealIPClientIPExtractor = errors.New("XRealIPClientIPExtractor error")
	ErrXRealIPAddrEmpty         = fmt.Errorf("%w: X-Real-IP empty", ErrXRealIPClientIPExtractor)
	ErrXRealIPAddrInvalid       = fmt.Errorf("%w: X-Real-IP invalid", ErrXRealIPClientIPExtractor)
)

type XRealIPClientIPExtractor struct{}

func (e XRealIPClientIPExtractor) GetClientIP(r *http.Request) (string, error) {
	ip := r.Header.Get(XRealIPHeader)
	if len(ip) == 0 {
		return "", ErrXRealIPAddrEmpty
	}
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if net.ParseIP(ip) == nil {
		return "", ErrXRealIPAddrInvalid
	}
	return ip, nil
}
