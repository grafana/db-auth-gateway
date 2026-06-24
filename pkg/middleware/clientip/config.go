// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"flag"
	"strings"
)

type ExtractorType string

const (
	ExtractorTypeRemoteAddr ExtractorType = "remoteaddr"
	ExtractorTypeXFF        ExtractorType = "xff"
	ExtractorTypeXRealIP    ExtractorType = "xrealip"
)

var (
	ExtractorTypePriority = [...]ExtractorType{ExtractorTypeXRealIP, ExtractorTypeRemoteAddr, ExtractorTypeXFF}
)

type Config struct {
	Enabled           bool
	Type              map[ExtractorType]bool
	TrustedProxyCount int
}

func (cfg *Config) RegisterFlags(f *flag.FlagSet) {
	cfg.Type = map[ExtractorType]bool{ExtractorTypeXFF: true}

	f.BoolVar(&cfg.Enabled,
		"client-ip-middleware.enabled",
		false,
		"Flag used to enable the Client IP Middleware.")
	f.IntVar(&cfg.TrustedProxyCount,
		"client-ip-middleware.trusted-proxy-count",
		2,
		"Specifies the number of trusted proxies. The client IP will be the next to the left of the last trusted proxy IP."+
			" This has no impact if the \"client-ip-middleware.extractor-type\" is set to \"remoteaddr\"")
	f.Func("client-ip-middleware.extractor-type", "Specifies a comma-separated list of types of IP extractors to be used. Supported extractor types are \"xff\", \"remoteaddr\" and \"xrealip\".", func(typeList string) error {
		cfg.Type = make(map[ExtractorType]bool, 0)
		typeListItems := strings.Split(typeList, ",")
		for _, t := range typeListItems {
			cfg.Type[ExtractorType(t)] = true
		}
		return nil
	})
}
