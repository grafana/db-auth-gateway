// SPDX-License-Identifier: AGPL-3.0-only

//go:build requires_docker

package services

import (
	"fmt"
	"os"
	"strconv"

	"github.com/grafana/e2e"
)

const (
	// DBAuthGatewayHTTPPort is the default dskit server HTTP listen port.
	DBAuthGatewayHTTPPort = 80
	// LokiHTTPPort is the default Loki HTTP listen port.
	LokiHTTPPort = 3100
	// MimirHTTPPort is the default Mimir HTTP listen port.
	MimirHTTPPort = 8080
	// TempoHTTPPort is the default Tempo HTTP listen port.
	TempoHTTPPort = 3200
	// TempoOTLPHTTPPort is the Tempo OTLP HTTP receiver port.
	TempoOTLPHTTPPort = 4318
	// ForwardAuthMockHTTPPort is the listen port of the forward-auth mock service.
	ForwardAuthMockHTTPPort = 8080
)

// DefaultDBAuthGatewayImage returns the Docker image to use for db-auth-gateway.
// Override with the DB_AUTH_GATEWAY_IMAGE environment variable.
func DefaultDBAuthGatewayImage() string {
	if img := os.Getenv("DB_AUTH_GATEWAY_IMAGE"); img != "" {
		return img
	}
	panic("No DB_AUTH_GATEWAY_IMAGE environment variable set. Please set it to the db-auth-gateway image to use for testing.")
}

// DefaultMimirImage returns the Docker image to use for Mimir.
// Override with the MIMIR_IMAGE environment variable.
//
// The default is an LBAC-capable weekly build (grafana/mimir#15554): LBAC, i.e. the
// -auth.label-access-control-enabled flag and X-Prom-Label-Policy enforcement, is not in
// any stable Mimir release yet, and the LBAC e2e test needs it. Switch this to a stable
// grafana/mimir:<version> tag once LBAC ships in a release. Keep this in sync with
// MIMIR_IMAGE in the Makefile.
func DefaultMimirImage() string {
	if img := os.Getenv("MIMIR_IMAGE"); img != "" {
		return img
	}
	return "grafana/mimir:r400-c18b9d72"
}

// MimirLBACEnabled reports whether the Mimir LBAC enforcement test should run, parsed from
// the MIMIR_LBAC_ENABLED environment variable (truthy values per strconv.ParseBool: "1",
// "t", "true", ...). Unset, empty, and explicitly false values ("0", "false") all disable
// it, so setting MIMIR_LBAC_ENABLED=false reliably turns the test off.
//
// It is false by default so that overriding MIMIR_IMAGE to a stable Mimir tag (which does
// not yet support LBAC) leaves the suite green. The default MIMIR_IMAGE is LBAC-capable,
// so set MIMIR_LBAC_ENABLED=true to run this test against it. When set true, MIMIR_IMAGE
// MUST point at an LBAC-capable build that defines -auth.label-access-control-enabled;
// otherwise Mimir rejects the flag and the test fails at startup.
func MimirLBACEnabled() bool {
	v, err := strconv.ParseBool(os.Getenv("MIMIR_LBAC_ENABLED"))
	return err == nil && v
}

// LokiLBACEnabled reports whether the Loki LBAC enforcement path should run, parsed from
// the LOKI_LBAC_ENABLED environment variable (same truthiness rules as MimirLBACEnabled).
//
// It is a separate gate from MimirLBACEnabled because Loki LBAC lives in a private Loki build:
// there is no public LBAC-capable Loki image to default to, so the default LOKI_IMAGE
// cannot enforce LBAC. Keeping it separate lets Mimir LBAC (MIMIR_LBAC_ENABLED) be enabled
// independently. When true, LOKI_IMAGE MUST point at an LBAC-capable Loki build; otherwise
// Loki rejects the lbac config key and the test fails at startup.
func LokiLBACEnabled() bool {
	v, err := strconv.ParseBool(os.Getenv("LOKI_LBAC_ENABLED"))
	return err == nil && v
}

// ForwardAuthMockImage returns the Docker image used for the forward-auth mock.
// Override with the FORWARD_AUTH_MOCK_IMAGE environment variable.
func ForwardAuthMockImage() string {
	if img := os.Getenv("FORWARD_AUTH_MOCK_IMAGE"); img != "" {
		return img
	}
	return "nginx:1.27-alpine"
}

// ForwardAuthConfig generates an nginx config that acts as a forward-auth mock,
// returning the specified orgID and label policy headers.
func ForwardAuthConfig(orgID, labelPolicy string) string {
	return fmt.Sprintf(`events {}
http {
  server {
    listen 8080;
    location / {
      add_header X-Scope-OrgID "%s" always;
      add_header X-Prom-Label-Policy "%s" always;
      return 200 "ok";
    }
  }
}`, orgID, labelPolicy)
}

// NewForwardAuthMock creates a forward-auth mock service. It runs nginx with the config
// at configPath (a path inside the container, e.g. under e2e.ContainerSharedDir), which is
// expected to answer every request with 200 plus the X-Scope-OrgID and X-Prom-Label-Policy
// response headers the gateway's forward_auth mode injects upstream.
func NewForwardAuthMock(name, configPath string) *e2e.HTTPService {
	return e2e.NewHTTPService(
		name,
		ForwardAuthMockImage(),
		e2e.NewCommandWithoutEntrypoint("nginx", "-c", configPath, "-g", "daemon off;"),
		e2e.NewHTTPReadinessProbe(ForwardAuthMockHTTPPort, "/", 200, 299),
		ForwardAuthMockHTTPPort,
	)
}

// NewDBAuthGateway creates a db-auth-gateway service with the given name and extra flags.
func NewDBAuthGateway(name string, flags map[string]string) *e2e.HTTPService {
	return e2e.NewHTTPService(
		name,
		DefaultDBAuthGatewayImage(),
		e2e.NewCommandWithoutEntrypoint("/usr/local/bin/db-auth-gateway", e2e.BuildArgs(e2e.MergeFlags(map[string]string{
			"-backend":   "mimir",
			"-log.level": "debug",
		}, flags))...),
		e2e.NewHTTPReadinessProbe(DBAuthGatewayHTTPPort, "/", 200, 299),
		DBAuthGatewayHTTPPort,
	)
}

// DefaultLokiImage returns the Docker image to use for Loki.
// Override with the LOKI_IMAGE environment variable.
func DefaultLokiImage() string {
	if img := os.Getenv("LOKI_IMAGE"); img != "" {
		return img
	}
	return "grafana/loki:3.7.1"
}

// NewLoki creates a Loki all-in-one service with the given name and extra flags.
func NewLoki(name string, flags map[string]string) *e2e.HTTPService {
	return e2e.NewHTTPService(
		name,
		DefaultLokiImage(),
		e2e.NewCommandWithoutEntrypoint("/usr/bin/loki", e2e.BuildArgs(e2e.MergeFlags(map[string]string{
			"-log.level": "warn",
		}, flags))...),
		e2e.NewHTTPReadinessProbe(LokiHTTPPort, "/ready", 200, 299),
		LokiHTTPPort,
	)
}

// DefaultTempoImage returns the Docker image to use for Tempo.
// Override with the TEMPO_IMAGE environment variable.
func DefaultTempoImage() string {
	if img := os.Getenv("TEMPO_IMAGE"); img != "" {
		return img
	}
	return "grafana/tempo:2.10.3"
}

// NewTempo creates a Tempo all-in-one service with the given name and extra flags.
func NewTempo(name string, flags map[string]string) *e2e.HTTPService {
	return e2e.NewHTTPService(
		name,
		DefaultTempoImage(),
		e2e.NewCommandWithoutEntrypoint("/tempo", e2e.BuildArgs(e2e.MergeFlags(map[string]string{
			"-target":          "all",
			"-log.level":       "warn",
			"-http-api-prefix": "/tempo",
		}, flags))...),
		e2e.NewHTTPReadinessProbe(TempoHTTPPort, "/ready", 200, 299),
		TempoHTTPPort,
		TempoOTLPHTTPPort,
	)
}

// NewMimir creates a Mimir all-in-one service with the given name and extra flags,
// using the default Mimir image.
func NewMimir(name string, flags map[string]string) *e2e.HTTPService {
	return NewMimirWithImage(name, DefaultMimirImage(), flags)
}

// NewMimirWithImage creates a Mimir all-in-one service with the given name, image, and
// extra flags. The explicit image lets callers run a specific build (e.g. one with LBAC
// support) rather than the default.
func NewMimirWithImage(name, image string, flags map[string]string) *e2e.HTTPService {
	return e2e.NewHTTPService(
		name,
		image,
		e2e.NewCommandWithoutEntrypoint("/bin/mimir", e2e.BuildArgs(e2e.MergeFlags(map[string]string{
			"-target":    "all",
			"-log.level": "warn",
		}, flags))...),
		e2e.NewHTTPReadinessProbe(MimirHTTPPort, "/ready", 200, 299),
		MimirHTTPPort,
	)
}
