// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/grafana/dskit/server"
)

// InstrumentationServerConfig configures and instrumentation server
type InstrumentationServerConfig struct {
	Port        int
	ReadTimeout time.Duration
}

// RegisterFlags adds the flags required to config this to the given FlagSet
func (cfg *InstrumentationServerConfig) RegisterFlags(f *flag.FlagSet) {
	f.IntVar(&cfg.Port, "instrumentation.server.port", 8001, "port to use for hosting instrumentation endpoints")
	f.DurationVar(&cfg.ReadTimeout, "instrumentation.server.read-timeout", 15*time.Second, "read timeout for the instrumentation endpoint")
}

// InstrumentationServer serves instrumentation endpoints on a different port
// than the default server
type InstrumentationServer struct {
	srv *http.Server
}

// NewInstrumentationServer returns an instrumentation server
func NewInstrumentationServer(cfg InstrumentationServerConfig) (*InstrumentationServer, error) {
	// Setup listeners first, so we can fail early if the port is in use.
	httpListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, err
	}

	router := mux.NewRouter()
	server.RegisterInstrumentation(router)

	srv := &http.Server{
		Handler:     router,
		ReadTimeout: cfg.ReadTimeout,
	}

	go func() {
		if err := srv.Serve(httpListener); err != nil {
			log.Printf("metrics server terminated, reason=%v", err)
		}
	}()

	return &InstrumentationServer{srv}, nil
}

// Stop closes the instrumentation server
func (m *InstrumentationServer) Stop() {
	_ = m.srv.Close()
}
