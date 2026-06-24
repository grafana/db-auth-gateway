// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/db-auth-gateway/pkg/auth"
)

func TestNewAuthenticator_Trust(t *testing.T) {
	a, err := NewAuthenticator(AuthConfig{Type: AuthTrust})
	require.NoError(t, err)
	assert.IsType(t, auth.TrustAuthenticator{}, a)
}

func TestNewAuthenticator_Forward(t *testing.T) {
	a, err := NewAuthenticator(AuthConfig{
		Type:    AuthForward,
		Forward: auth.ForwardConfig{URL: "http://auth.example/verify"},
	})
	require.NoError(t, err)
	assert.IsType(t, &auth.ForwardAuthenticator{}, a)
}

func TestNewAuthenticator_ForwardMissingURL(t *testing.T) {
	a, err := NewAuthenticator(AuthConfig{Type: AuthForward})
	require.Error(t, err)
	assert.Nil(t, a)
	assert.Contains(t, err.Error(), "invalid forward-auth.url")
}

func TestNewAuthenticator_InvalidType(t *testing.T) {
	a, err := NewAuthenticator(AuthConfig{Type: "bogus"})
	require.Error(t, err)
	assert.Nil(t, a)
	assert.Contains(t, err.Error(), `"bogus"`)
}

func TestAuthConfig_RegisterFlags(t *testing.T) {
	cfg := AuthConfig{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg.RegisterFlags(fs)

	for _, name := range []string{"auth.type", "forward-auth.url", "forward-auth.timeout", "forward-auth.cache-ttl"} {
		assert.NotNil(t, fs.Lookup(name), "flag %q should be registered", name)
	}

	require.NoError(t, fs.Parse([]string{
		"-auth.type=forward_auth",
		"-forward-auth.url=http://auth.example/verify",
		"-forward-auth.timeout=2s",
		"-forward-auth.cache-ttl=30s",
	}))

	assert.Equal(t, AuthForward, cfg.Type)
	assert.Equal(t, "http://auth.example/verify", cfg.Forward.URL)
	assert.Equal(t, 2*time.Second, cfg.Forward.Timeout)
	assert.Equal(t, 30*time.Second, cfg.Forward.CacheTTL)
}

func TestAuthConfig_RegisterFlags_Defaults(t *testing.T) {
	cfg := AuthConfig{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg.RegisterFlags(fs)
	require.NoError(t, fs.Parse(nil))

	assert.Equal(t, AuthTrust, cfg.Type)
	assert.Equal(t, "", cfg.Forward.URL)
	assert.Equal(t, 5*time.Second, cfg.Forward.Timeout)
	assert.Equal(t, time.Duration(0), cfg.Forward.CacheTTL)
}
