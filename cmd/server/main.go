package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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
	// 1. Store — in-memory source of truth for this demonstration.
	memStore := store.NewMemoryStore()

	memStore.Create(context.Background(), &tenant.Tenant{
		ID:    "acme",
		State: tenant.Active,
		Roles: []string{"admin"},
	})
	memStore.Create(context.Background(), &tenant.Tenant{
		ID:    "globex",
		State: tenant.Active,
		Roles: []string{"viewer"},
	})

	cachedStore := store.NewCachedStore(memStore, 10*time.Second)

	// 2. Resolver — identifies the tenant from the subdomain.
	//    E.g. acme.localhost:8080 → TenantID("acme")
	subdomainResolver := resolver.NewSubdomainResolver("localhost")

	// 3. Manager — assembles Resolver + Store.
	manager := tenant.New(
		tenant.WithResolver(subdomainResolver),
		tenant.WithStore(cachedStore),
	)

	// 4. RBAC — demonstration of permissions that differ per tenant.
	authz := rbac.New()
	authz.DefineRole("acme", "admin", []string{"users:read", "users:write"})
	authz.DefineRole("globex", "viewer", []string{"users:read"})

	// 5. Application routes.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		t := tenantctx.FromContext(r.Context())
		if t == nil {
			http.Error(w, "no tenant in context", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
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
		json.NewEncoder(w).Encode(map[string]string{"message": "user list would go here"})
	})

	// 6. Middleware — injects the resolved tenant into the context of each
	//    request, via the net/http adapter (our reference adapter).
	handler := nethttp.Wrap(manager, mux)

	// 7. Graceful shutdown — listen for SIGINT/SIGTERM via the standard
	// signal.NotifyContext, the idiomatic replacement for a manual
	// signal.Notify channel: ctx is canceled the moment either signal
	// arrives.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:    ":8080",
		Handler: handler,
	}

	go func() {
		log.Println("listening on :8080")
		log.Println(`try: curl -H "Host: acme.localhost" http://localhost:8080/api/me`)
		log.Println(`try: curl -H "Host: globex.localhost" http://localhost:8080/api/users  (expects 403 — globex only has users:read)`)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	stop()
	log.Println("shutdown signal received")

	// Server.Shutdown already refuses new connections and waits for
	// in-flight requests to finish on its own — this timeout only bounds
	// how long we're willing to wait for that before giving up.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	log.Println("shutting down server...")
	err := server.Shutdown(shutdownCtx)

	switch {
	case err == nil:
		log.Println("server shut down cleanly")
	case errors.Is(shutdownCtx.Err(), context.DeadlineExceeded):
		// Shutdown() returns ctx.Err() in this case (i.e. this same
		// DeadlineExceeded) — not a fatal condition, just a clear warning
		// that some in-flight requests may not have finished before we
		// gave up waiting.
		log.Printf("WARNING: shutdown did not complete within %s, exiting anyway", shutdownTimeout)
	default:
		log.Fatalf("shutdown error: %v", err)
	}
}
