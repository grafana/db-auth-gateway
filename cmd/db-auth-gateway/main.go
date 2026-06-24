// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors/version"
	promVersion "github.com/prometheus/common/version"

	"github.com/grafana/db-auth-gateway/pkg/gateway/libmain"
)

// Version is set via build flag -ldflags -X main.Version
var (
	Version  string
	Branch   string
	Revision string
)

func main() {
	promVersion.Version = Version
	promVersion.Branch = Branch
	promVersion.Revision = Revision
	prometheus.MustRegister(version.NewCollector("db_auth_gateway"))

	libmain.Main()
}
