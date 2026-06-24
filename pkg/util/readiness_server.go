// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gorilla/mux"
)

const (
	DefaultReadinessServerPort = 8002
)

// ReadinessServerConfig configures the readiness endpoint server.
type ReadinessServerConfig struct {
	Enabled     bool
	Port        int
	ReadTimeout time.Duration
}

// RegisterFlags adds the flags required to config this to the given FlagSet.
func (cfg *ReadinessServerConfig) RegisterFlags(f *flag.FlagSet) {
	f.BoolVar(&cfg.Enabled, "readiness.server.enabled", false, "enable a separate dedicated readiness endpoint")
	f.IntVar(&cfg.Port,
		"readiness.server.port",
		DefaultReadinessServerPort,
		"port to use for hosting the readiness endpoint")
	f.DurationVar(&cfg.ReadTimeout, "readiness.server.read-timeout", 15*time.Second, "read timeout for the readiness endpoint")
}

// ReadinessServer serves a dedicated readiness endpoint on a configurable port.
type ReadinessServer struct {
	srv *http.Server
}

// NewReadinessServer returns a readiness server.
func NewReadinessServer(cfg ReadinessServerConfig, handler http.Handler, logger log.Logger) (*ReadinessServer, error) {
	if !cfg.Enabled {
		return &ReadinessServer{nil}, nil
	}

	httpListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, err
	}

	router := mux.NewRouter()
	router.Path("/").Methods("GET").Handler(handler)

	srv := &http.Server{
		Handler:     router,
		ReadTimeout: cfg.ReadTimeout,
	}

	go func() {
		if err := srv.Serve(httpListener); err != nil {
			level.Error(logger).Log("msg", "readiness server terminated", "err", err)
		}
	}()

	return &ReadinessServer{srv}, nil
}

// Stop closes the readiness server.
func (m *ReadinessServer) Stop() {
	if m.srv != nil {
		_ = m.srv.Close()
	}
}
