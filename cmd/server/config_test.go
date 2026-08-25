package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePort_AbsentUsesDefault(t *testing.T) {
	port, err := parsePort()
	require.NoError(t, err)
	assert.Equal(t, defaultPort, port)
}

func TestParsePort_ValidValue(t *testing.T) {
	t.Setenv("PORT", "9090")

	port, err := parsePort()
	require.NoError(t, err)
	assert.Equal(t, 9090, port)
}

func TestParsePort_Invalid(t *testing.T) {
	cases := []string{"abc", "", "0", "-1", "65536", "8080.5"}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("PORT", raw)

			_, err := parsePort()
			assert.Error(t, err)
		})
	}
}

func TestParsePort_BoundaryValuesAreValid(t *testing.T) {
	t.Setenv("PORT", "1")
	port, err := parsePort()
	require.NoError(t, err)
	assert.Equal(t, 1, port)

	t.Setenv("PORT", "65535")
	port, err = parsePort()
	require.NoError(t, err)
	assert.Equal(t, 65535, port)
}

func TestParseTenantBaseDomain_AbsentUsesDefault(t *testing.T) {
	domain, err := parseTenantBaseDomain()
	require.NoError(t, err)
	assert.Equal(t, defaultTenantBaseDomain, domain)
}

func TestParseTenantBaseDomain_ValidValue(t *testing.T) {
	t.Setenv("TENANT_BASE_DOMAIN", "example.com")

	domain, err := parseTenantBaseDomain()
	require.NoError(t, err)
	assert.Equal(t, "example.com", domain)
}

func TestParseTenantBaseDomain_EmptyIsError(t *testing.T) {
	// Present but explicitly empty must fail — distinct from absent,
	// which is why parseTenantBaseDomain uses os.LookupEnv rather than
	// os.Getenv.
	t.Setenv("TENANT_BASE_DOMAIN", "")

	_, err := parseTenantBaseDomain()
	assert.Error(t, err)
}

func TestParseCacheTTLSeconds_AbsentUsesDefault(t *testing.T) {
	seconds, err := parseCacheTTLSeconds()
	require.NoError(t, err)
	assert.Equal(t, defaultCacheTTLSeconds, seconds)
}

func TestParseCacheTTLSeconds_ValidValue(t *testing.T) {
	t.Setenv("CACHE_TTL_SECONDS", "30")

	seconds, err := parseCacheTTLSeconds()
	require.NoError(t, err)
	assert.Equal(t, 30, seconds)
}

func TestParseCacheTTLSeconds_Invalid(t *testing.T) {
	cases := []string{"abc", "", "0", "-5", "10.5"}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("CACHE_TTL_SECONDS", raw)

			_, err := parseCacheTTLSeconds()
			assert.Error(t, err)
		})
	}
}

func TestLoadServerConfig_AllAbsentUsesDefaults(t *testing.T) {
	cfg, err := loadServerConfig()
	require.NoError(t, err)

	assert.Equal(t, defaultPort, cfg.port)
	assert.Equal(t, defaultTenantBaseDomain, cfg.tenantBaseDomain)
	assert.Equal(t, time.Duration(defaultCacheTTLSeconds)*time.Second, cfg.cacheTTL)
}

func TestLoadServerConfig_PropagatesFirstInvalidVariable(t *testing.T) {
	t.Setenv("PORT", "not-a-number")

	_, err := loadServerConfig()
	assert.Error(t, err)
}
