// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/runtimeconfig"
	"github.com/grafana/dskit/services"
	"github.com/prometheus/client_golang/prometheus"
	"go.yaml.in/yaml/v3"

	"github.com/grafana/db-auth-gateway/pkg/router"
)

var errMultipleDocuments = errors.New("the provided runtime configuration contains multiple documents")

// APIOverridesConfig holds configuration for per-tenant overrides hot-reload.
type APIOverridesConfig struct {
	Path         string        `yaml:"path"`
	ReloadPeriod time.Duration `yaml:"reload_period"`
}

// Overrides contains global and per-tenant overrides to block access to different types of
// endpoints for each supported backend. This struct is the one used when loading an overrides
// config file.
type Overrides struct {
	MimirGlobalOverrides *MimirGlobalOverrides `yaml:"mimir_global"`

	LokiTenantOverrides  map[string]*LokiOverrides        `yaml:"loki"`
	MimirTenantOverrides map[string]*MimirTenantOverrides `yaml:"mimir"`
}

// RuntimeConfigTenantOverrides provides per-tenant overrides based on a runtimeconfig.Manager that reads
// limits from a configuration file on disk and periodically reloads them.
type RuntimeConfigTenantOverrides struct {
	manager *runtimeconfig.Manager
}

// NewRuntimeOverrides creates a new TenantOverrides that loads global and per-tenant overrides from a runtimeconfig.Manager
func NewRuntimeOverrides(manager *runtimeconfig.Manager) *RuntimeConfigTenantOverrides {
	return &RuntimeConfigTenantOverrides{
		manager: manager,
	}
}

func (r *RuntimeConfigTenantOverrides) GetLokiConfig(user string) LokiOverrides {
	var cfg interface{}
	if r.manager != nil {
		cfg = r.manager.GetConfig()
		if cfg == nil {
			return defaultLokiOverrides
		}
	}
	pto, ok := cfg.(*Overrides)
	if ok && pto != nil && pto.LokiTenantOverrides[user] != nil {
		return *pto.LokiTenantOverrides[user]
	}

	return defaultLokiOverrides
}

func (r *RuntimeConfigTenantOverrides) GetMimirConfig(user string) (MimirGlobalOverrides, MimirTenantOverrides) {
	global := defaultMimirGlobalOverrides
	tenant := defaultMimirTenantOverrides

	var cfg interface{}
	if r.manager != nil {
		cfg = r.manager.GetConfig()
		if cfg == nil {
			return global, tenant
		}
	}
	pto, ok := cfg.(*Overrides)
	if ok && pto != nil {
		if pto.MimirGlobalOverrides != nil {
			global = *pto.MimirGlobalOverrides
		}
		if pto.MimirTenantOverrides[user] != nil {
			tenant = *pto.MimirTenantOverrides[user]
		}
	}

	return global, tenant
}

func loadRuntimeConfig(r io.Reader) (interface{}, error) {
	overrides := &Overrides{}

	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)

	// Decode the first document. An empty document (EOF) is OK.
	if err := decoder.Decode(overrides); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	// Ensure the provided YAML config is not composed of multiple documents,
	if err := decoder.Decode(&Overrides{}); !errors.Is(err, io.EOF) {
		return nil, errMultipleDocuments
	}

	return overrides, nil
}

func initTenantOverrides(
	cfg APIOverridesConfig,
	logger log.Logger,
	reg prometheus.Registerer,
) (*RuntimeConfigTenantOverrides, error) {
	// Ensure we have a non-empty path before wrapping it in a slice since runtime config
	// treats an empty slice as an error but NOT a single element slice of an empty string
	var paths []string
	if cfg.Path != "" {
		paths = strings.Split(cfg.Path, ",")
	}

	serv, err := runtimeconfig.New(
		runtimeconfig.Config{LoadPath: paths, ReloadPeriod: cfg.ReloadPeriod, Loader: loadRuntimeConfig},
		"gateway-tenant-overrides",
		prometheus.WrapRegistererWithPrefix("gateway_", reg),
		logger,
	)
	if err != nil {
		// The loadpath was likely empty, but we still can init the struct and have the overrides
		// middleware use the defaults for everything
		return NewRuntimeOverrides(nil), nil
	}

	// The gateway runtime config just delegates to RuntimeConfig and doesn't have any state or need to do
	// anything in the start/stopping phase. Thus we can create it as part of tenant runtime config
	// setup without any service instance of its own.
	if err := services.StartAndAwaitRunning(context.Background(), serv); err != nil {
		level.Error(logger).Log("msg", "error starting overrides service", "err", err)
		return nil, err
	}
	return NewRuntimeOverrides(serv), nil
}

func buildRoutesMap(routes []router.Route) map[string]bool {
	result := map[string]bool{}
	for _, p := range routes {
		result[p.Path] = true
	}
	return result
}
