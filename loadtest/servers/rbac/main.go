// Command rbac is Load Test Scenario C: identical to Scenario B
// (tenantcore), plus an RBAC permission check on every request. Compare
// its numbers to Scenario B's to isolate the additional cost of RBAC
// specifically, independently of tenant resolution.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/rbac"
	"github.com/sylvinhio676-ux/tenant-core/resolver"
	"github.com/sylvinhio676-ux/tenant-core/store"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	nethttp "github.com/sylvinhio676-ux/tenant-core/middleware/nethttp"
)

func main() {
	memStore := store.NewMemoryStore()

	// Fixed, hardcoded IDs known not to collide — Create failing here would
	// be a programming bug in this load-test server, not a runtime
	// condition worth handling.
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "acme", State: tenant.Active, Roles: []string{"admin"}})
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "globex", State: tenant.Active, Roles: []string{"viewer"}})
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "initech", State: tenant.Active, Roles: []string{"viewer"}})

	cachedStore := store.NewCachedStore(memStore, 10*time.Second)

	subdomainResolver := resolver.NewSubdomainResolver("localhost")

	manager := tenant.New(
		tenant.WithResolver(subdomainResolver),
		tenant.WithStore(cachedStore),
	)

	authz := rbac.New()
	authz.DefineRole("acme", "admin", []string{"users:read", "users:write"})
	authz.DefineRole("globex", "viewer", []string{"users:read"})
	authz.DefineRole("initech", "viewer", []string{"users:read"})

	mux := http.NewServeMux()

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
		_ = json.NewEncoder(w).Encode(map[string]string{"tenant_id": string(t.ID)})
	})

	handler := nethttp.Wrap(manager, mux)

	log.Println("Scenario C (tenant-core + RBAC) listening on :8083")
	log.Println(`try: curl -H "Host: acme.localhost" http://localhost:8083/api/users`)

	if err := http.ListenAndServe(":8083", handler); err != nil {
		log.Fatal(err)
	}
}
