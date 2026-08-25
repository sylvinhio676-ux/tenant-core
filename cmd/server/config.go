package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultPort             = 8080
	defaultTenantBaseDomain = "localhost"
	defaultCacheTTLSeconds  = 10
)

// serverConfig holds the reference server's runtime configuration, resolved
// from environment variables with sensible defaults.
type serverConfig struct {
	port             int
	tenantBaseDomain string
	cacheTTL         time.Duration
}

// loadServerConfig resolves PORT, TENANT_BASE_DOMAIN, and CACHE_TTL_SECONDS
// from the environment. A variable that is absent uses its default; a
// variable that is present but invalid (including empty) is a
// configuration error. Returning the error, rather than calling
// log.Fatalf directly, keeps this function testable in isolation — main()
// is responsible for treating a non-nil error as fatal.
func loadServerConfig() (serverConfig, error) {
	port, err := parsePort()
	if err != nil {
		return serverConfig{}, err
	}

	domain, err := parseTenantBaseDomain()
	if err != nil {
		return serverConfig{}, err
	}

	ttlSeconds, err := parseCacheTTLSeconds()
	if err != nil {
		return serverConfig{}, err
	}

	return serverConfig{
		port:             port,
		tenantBaseDomain: domain,
		cacheTTL:         time.Duration(ttlSeconds) * time.Second,
	}, nil
}

// parsePort resolves PORT: absent -> defaultPort; present -> must be an
// integer in [1, 65535].
func parsePort() (int, error) {
	raw, present := os.LookupEnv("PORT")
	if !present {
		return defaultPort, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid PORT: %q (must be an integer between 1 and 65535)", raw)
	}
	return port, nil
}

// parseTenantBaseDomain resolves TENANT_BASE_DOMAIN: absent ->
// defaultTenantBaseDomain; present -> must be non-empty.
func parseTenantBaseDomain() (string, error) {
	raw, present := os.LookupEnv("TENANT_BASE_DOMAIN")
	if !present {
		return defaultTenantBaseDomain, nil
	}

	if raw == "" {
		return "", fmt.Errorf("invalid TENANT_BASE_DOMAIN: must not be empty")
	}
	return raw, nil
}

// parseCacheTTLSeconds resolves CACHE_TTL_SECONDS: absent ->
// defaultCacheTTLSeconds; present -> must be a strictly positive integer.
func parseCacheTTLSeconds() (int, error) {
	raw, present := os.LookupEnv("CACHE_TTL_SECONDS")
	if !present {
		return defaultCacheTTLSeconds, nil
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("invalid CACHE_TTL_SECONDS: %q (must be a positive integer)", raw)
	}
	return seconds, nil
}
