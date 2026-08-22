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
	// 1. Store — source de vérité en mémoire pour cette démonstration.
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

	// 2. Resolver — identifie le tenant depuis le sous-domaine.
	//    Ex: acme.localhost:8080 → TenantID("acme")
	subdomainResolver := resolver.NewSubdomainResolver("localhost")

	// 3. Manager — assemble Resolver + Store.
	manager := tenant.New(
		tenant.WithResolver(subdomainResolver),
		tenant.WithStore(cachedStore),
	)

	// 4. RBAC — démonstration de permissions différenciées par tenant.
	authz := rbac.New()
	authz.DefineRole("acme", "admin", []string{"users:read", "users:write"})
	authz.DefineRole("globex", "viewer", []string{"users:read"})

	// 5. Routes applicatives.
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

	// 6. Middleware — injecte le tenant résolu dans le contexte de chaque
	//    requête, via l'adaptateur net/http (notre adaptateur de référence).
	handler := nethttp.Wrap(manager, mux)

	log.Println("listening on :8080")
	log.Println(`try: curl -H "Host: acme.localhost" http://localhost:8080/api/me`)
	log.Println(`try: curl -H "Host: globex.localhost" http://localhost:8080/api/users  (expects 403 — globex only has users:read)`)

	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatal(err)
	}
}