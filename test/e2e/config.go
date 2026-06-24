// SPDX-License-Identifier: AGPL-3.0-only

//go:build requires_docker

package e2e

import (
	"os"
	"path/filepath"

	"github.com/grafana/e2e"
)

const (
	defaultNetworkName = "db-auth-gateway-e2e-test"
)

// networkName is the Docker network name for e2e tests.
var networkName = func() string {
	if n := os.Getenv("E2E_NETWORK_NAME"); n != "" {
		return n
	}
	return defaultNetworkName
}()

// writeFileToSharedDir writes content to a path relative to the scenario shared directory.
func writeFileToSharedDir(s *e2e.Scenario, dst string, content []byte) error {
	dst = filepath.Join(s.SharedDir(), dst)
	if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return err
	}
	return os.WriteFile(dst, content, os.ModePerm)
}
