package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/rbac"
	"github.com/sylvinhio676-ux/tenant-core/resolver"
	"github.com/sylvinhio676-ux/tenant-core/store"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	nethttp "github.com/sylvinhio676-ux/tenant-core/middleware/nethttp"
)

// shutdownTimeout bounds how long graceful shutdown waits for in-flight
// requests to finish before giving up. This lives in cmd/server, the
// toolkit's reference server — tenant-core itself never manages OS
// signals or process lifecycle, only its own middleware/store/RBAC logic.
const shutdownTimeout = 10 * time.Second

func main() {
	// 0. Configuration — resolved from the environment (PORT,
	// TENANT_BASE_DOMAIN, CACHE_TTL_SECONDS, LOG_FORMAT), falling back to
	// defaults when a variable is absent. A variable that is present but
	// invalid is a configuration error, caught here before anything else
	// starts.
	//
	// This one error path unavoidably logs through slog's built-in default
	// logger rather than the configured one: cfg.logFormat is exactly what
	// failed to resolve, so there is no chosen format yet to honor.
	cfg, err := loadServerConfig()
	if err != nil {
		slog.Error("invalid configuration", "component", "config", "error", err)
		os.Exit(1)
	}

	// slog.SetDefault makes slog.Info/Warn/Error usable as package-level
	// functions everywhere below, without threading a *slog.Logger through
	// every call site.
	var logHandler slog.Handler
	switch cfg.logFormat {
	case "json":
		logHandler = slog.NewJSONHandler(os.Stdout, nil)
	default: // "text" — the only other value parseLogFormat accepts
		logHandler = slog.NewTextHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(logHandler))

	slog.Info("configuration loaded",
		"component", "config",
		"port", cfg.port,
		"tenant_base_domain", cfg.tenantBaseDomain,
		"cache_ttl_seconds", int(cfg.cacheTTL.Seconds()),
		"log_format", cfg.logFormat,
	)

	// 1. Store — in-memory source of truth for this demonstration.
	memStore := store.NewMemoryStore()

	// Fixed, hardcoded IDs known not to collide — Create failing here would
	// be a programming bug in this reference server, not a runtime
	// condition worth handling.
	_ = memStore.Create(context.Background(), &tenant.Tenant{
		ID:    "acme",
		State: tenant.Active,
		Roles: []tenant.Role{"admin"},
	})
	_ = memStore.Create(context.Background(), &tenant.Tenant{
		ID:    "globex",
		State: tenant.Active,
		Roles: []tenant.Role{"viewer"},
	})

	cachedStore := store.NewCachedStore(memStore, cfg.cacheTTL)

	// 2. Resolver — identifies the tenant from the subdomain.
	//    E.g. acme.<TENANT_BASE_DOMAIN>:<PORT> → TenantID("acme")
	subdomainResolver := resolver.NewSubdomainResolver(cfg.tenantBaseDomain)

	// 3. Manager — assembles Resolver + Store.
	manager := tenant.New(
		tenant.WithResolver(subdomainResolver),
		tenant.WithStore(cachedStore),
	)

	// 4. RBAC — demonstration of permissions that differ per tenant.
	authz := rbac.New()
	authz.DefineRole("acme", "admin", "users:read", "users:write")
	authz.DefineRole("globex", "viewer", "users:read")

	// 5. Application routes.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		t := tenantctx.FromContext(r.Context())
		if t == nil {
			http.Error(w, "no tenant in context", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id":    t.ID,
			"tenant_state": t.State,
			"roles":        t.Roles,
		})
	})

	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		t := tenantctx.FromContext(r.Context())
		if t == nil {
			http.Error(w, "no tenant in context", http.StatusInternalServerError)
			return
		}

		if !authz.Can(t, "users:write") {
			http.Error(w, "forbidden: users:write required", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "user list would go here"})
	})

	// 6. Middleware — injects the resolved tenant into the context of each
	//    request, via the net/http adapter (our reference adapter).
	tenantHandler := nethttp.Wrap(manager, mux)

	// 6b. Top-level router: /healthz and /readyz are registered here,
	// OUTSIDE tenantHandler, and never go through tenant resolution.
	// Health/readiness probes hit the server directly (by pod IP, not by
	// tenant subdomain) and must never fail just because their request
	// doesn't carry a Host header that resolves to a tenant — everything
	// else falls through to the tenant-aware handler.
	handler := http.NewServeMux()
	handler.HandleFunc("GET /healthz", healthzHandler)
	handler.HandleFunc("GET /readyz", readyzHandler)
	handler.Handle("/", tenantHandler)

	// 7. Graceful shutdown — listen for SIGINT/SIGTERM via the standard
	// signal.NotifyContext, the idiomatic replacement for a manual
	// signal.Notify channel: ctx is canceled the moment either signal
	// arrives.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf(":%d", cfg.port)
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		// The two example curl commands are folded into fields, rather
		// than printed as separate free-form lines, so they stay part of
		// the same structured event under LOG_FORMAT=json instead of
		// becoming stray unstructured output mixed into a JSON stream.
		slog.Info("server listening",
			"component", "server",
			"addr", addr,
			"example_me", fmt.Sprintf(`curl -H "Host: acme.%s" http://localhost:%d/api/me`, cfg.tenantBaseDomain, cfg.port),
			"example_users", fmt.Sprintf(`curl -H "Host: globex.%s" http://localhost:%d/api/users`, cfg.tenantBaseDomain, cfg.port),
		)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "component", "server", "error", err)
			os.Exit(1)
		}
	}()

	// Mark the server ready right after starting it — see health.go for
	// what /readyz does with this.
	ready.Store(true)

	<-ctx.Done()

	// First step of shutdown, before anything else: mark not-ready so
	// /readyz starts returning 503 immediately, giving a load balancer a
	// chance to stop routing new traffic here before Shutdown actually
	// begins draining connections below.
	ready.Store(false)

	stop()
	// signal.NotifyContext only cancels ctx on either signal; it doesn't
	// expose which one actually fired, so this can't name it without
	// switching to a manual signal.Notify channel instead.
	slog.Info("shutdown signal received", "component", "shutdown")

	// Server.Shutdown already refuses new connections and waits for
	// in-flight requests to finish on its own — this timeout only bounds
	// how long we're willing to wait for that before giving up.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	slog.Info("shutting down server", "component", "shutdown")
	err = server.Shutdown(shutdownCtx)

	switch {
	case err == nil:
		slog.Info("server shut down cleanly", "component", "shutdown")
	case errors.Is(shutdownCtx.Err(), context.DeadlineExceeded):
		// Shutdown() returns ctx.Err() in this case (i.e. this same
		// DeadlineExceeded) — not a fatal condition, just a clear warning
		// that some in-flight requests may not have finished before we
		// gave up waiting.
		slog.Warn("shutdown did not complete within timeout", "component", "shutdown", "timeout", shutdownTimeout)
	default:
		slog.Error("shutdown error", "component", "shutdown", "error", err)
		os.Exit(1)
	}
}
