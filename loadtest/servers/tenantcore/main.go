// Command tenantcore is Load Test Scenario B: the same
// Resolver → CachedStore → Manager → tenantctx pipeline as cmd/server,
// WITHOUT RBAC, so its measured cost isolates exactly the tenant
// resolution path. Compare against Scenario A (httponly) to see what
// this path costs on top of the raw net/http baseline, and against
// Scenario C (rbac) to see what RBAC costs on top of this.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	// Registers pprof's handlers (/debug/pprof/...) on http.DefaultServeMux.
	// Served on its own port below — never on the application's own mux —
	// so a profiling session can never accidentally become part of the
	// load-tested traffic on :8082.
	_ "net/http/pprof"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/resolver"
	"github.com/sylvinhio676-ux/tenant-core/store"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	nethttp "github.com/sylvinhio676-ux/tenant-core/middleware/nethttp"
)

func main() {
	memStore := store.NewMemoryStore()

	// Several tenants are seeded even though a single-tenant run only
	// ever targets "acme" — this leaves the store ready for a future
	// multi-tenant load scenario without changing this server.
	// Fixed, hardcoded IDs known not to collide — Create failing here would
	// be a programming bug in this load-test server, not a runtime
	// condition worth handling.
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "acme", State: tenant.Active})
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "globex", State: tenant.Active})
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "initech", State: tenant.Active})

	cachedStore := store.NewCachedStore(memStore, 10*time.Second)

	subdomainResolver := resolver.NewSubdomainResolver("localhost")

	manager := tenant.New(
		tenant.WithResolver(subdomainResolver),
		tenant.WithStore(cachedStore),
	)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		t := tenantctx.FromContext(r.Context())
		if t == nil {
			http.Error(w, "no tenant in context", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"tenant_id": string(t.ID)})
	})

	handler := nethttp.Wrap(manager, mux)

	go func() {
		log.Println("pprof listening on :6061 (http://localhost:6061/debug/pprof/)")
		if err := http.ListenAndServe(":6061", nil); err != nil {
			log.Printf("pprof server error: %v", err)
		}
	}()

	log.Println("Scenario B (tenant-core, no RBAC) listening on :8082")
	log.Println(`try: curl -H "Host: acme.localhost" http://localhost:8082/api/me`)

	if err := http.ListenAndServe(":8082", handler); err != nil {
		log.Fatal(err)
	}
}
