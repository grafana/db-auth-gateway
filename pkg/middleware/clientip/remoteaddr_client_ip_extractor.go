// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

type RemoteAddrClientIPExtractor struct{}

var (
	ErrRemoteAddrClientIPExtractor = errors.New("RemoteAddrClientIPExtractor error")
	ErrRemoteAddrEmpty             = fmt.Errorf("%w: RemoteAddr empty", ErrRemoteAddrClientIPExtractor)
	ErrRemoteAddrInvalid           = fmt.Errorf("%w: RemoteAddr invalid", ErrRemoteAddrClientIPExtractor)
)

func (c RemoteAddrClientIPExtractor) GetClientIP(r *http.Request) (string, error) {
	if len(r.RemoteAddr) == 0 {
		return "", ErrRemoteAddrEmpty
	}

	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}

	if net.ParseIP(ip) == nil {
		return "", ErrRemoteAddrInvalid
	}

	return ip, nil
}
