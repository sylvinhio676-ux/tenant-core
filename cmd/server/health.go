package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// ready reports whether the server is currently ready to serve traffic.
// It is set to true once the server has finished starting up (just after
// ListenAndServe is launched), and back to false as the very first step
// of graceful shutdown, before Server.Shutdown is called.
var ready atomic.Bool

/*
 * healthzHandler and readyzHandler — liveness vs. readiness.
 *
 * Honest limitation of this reference server: today, /healthz and /readyz
 * behave identically for almost the server's entire lifetime. Startup
 * (steps 1-6 in main) completes before ListenAndServe is even called, so
 * there is effectively no window where the process is alive but not yet
 * ready. The one moment they actually diverge is shutdown: ready.Store(false)
 * runs before Server.Shutdown is called, so /readyz starts returning 503
 * — telling a load balancer to stop sending new traffic — while /healthz
 * keeps reporting 200 for as long as the process is still up and able to
 * answer at all. A future version of this server with real external
 * dependencies (a database, Redis) could make the two diverge more
 * meaningfully: /healthz could still report ok (the process itself is
 * fine) while /readyz reports not-ready because a dependency is
 * temporarily unreachable.
 */

// healthzHandler is a liveness probe: it returns 200 as long as the HTTP
// server itself is answering requests, regardless of readiness.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// readyzHandler is a readiness probe: 200 while ready is true, 503 once
// it has been set to false (e.g. during shutdown).
func readyzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "not ready"})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}
