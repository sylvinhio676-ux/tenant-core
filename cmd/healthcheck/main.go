// Command healthcheck is a tiny, standalone diagnostic tool for Docker's
// HEALTHCHECK: it checks whether a locally running cmd/server instance
// answers GET /healthz with 200, then exits 0 (healthy) or 1 (unhealthy).
//
// It exists because the distroless base image used for cmd/server's
// container has no shell and no curl/wget — HEALTHCHECK needs an actual
// executable it can run directly, not a shell command line.
//
// This is deliberately minimal and has no dependency on tenant-core
// itself: it isn't the server, it's an external probe of the server, so
// parsing PORT here just needs a sane default, not the strict fail-fast
// validation cmd/server/config.go applies to its own configuration.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	defaultPort    = 8080
	requestTimeout = 3 * time.Second
)

func main() {
	port := defaultPort
	if raw := os.Getenv("PORT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			port = parsed
		}
		// An invalid PORT here just falls back to defaultPort — this tool
		// probes whatever cmd/server actually ended up listening on, and
		// isn't responsible for validating that configuration itself.
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)

	client := &http.Client{Timeout: requestTimeout}

	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: request to %s failed: %v\n", url, err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s returned status %d, want 200\n", url, resp.StatusCode)
		os.Exit(1)
	}

	os.Exit(0)
}
