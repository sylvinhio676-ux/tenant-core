# Getting started

[🇬🇧 English](GETTING_STARTED.md) · 🇫🇷 Français

Guide pratique pour intégrer tenant-core dans votre propre projet, étape par
étape. Chaque palier fonctionne seul — vous pouvez vous arrêter à n'importe
lequel et avoir quelque chose de fonctionnel. Pour comprendre les décisions
d'architecture derrière chaque composant (concurrence, trade-offs, garanties),
voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) (en anglais ; version
française : [docs/ARCHITECTURE.fr.md](docs/ARCHITECTURE.fr.md)) — ce
document-ci ne fait que du "comment faire".

## 1. Installation

```bash
go get github.com/sylvinhio676-ux/tenant-core@v0.2.0
```

## 2. Le strict minimum : résoudre un tenant

Trois briques suffisent pour qu'une requête HTTP sache "de quel tenant"
elle vient :

- un `Store` — où sont stockés vos tenants (ici `store.MemoryStore`, pour
  démarrer ; en production ce sera votre implémentation de l'interface
  `tenant.Store`, ex. Postgres) ;
- un `Resolver` — comment identifier le tenant depuis la requête (ici
  `resolver.SubdomainResolver`, basé sur le sous-domaine) ;
- un `Manager` — assemble les deux, et un middleware qui l'utilise.

```go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/resolver"
	"github.com/sylvinhio676-ux/tenant-core/store"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	nethttp "github.com/sylvinhio676-ux/tenant-core/middleware/nethttp"
)

func main() {
	// 1. Store: source of truth for tenant data. In production, replace
	// this with your own implementation of the tenant.Store interface
	// (Postgres, etc.) — everything else below stays identical.
	memStore := store.NewMemoryStore()
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "acme", State: tenant.Active})
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "globex", State: tenant.Active})

	// 2. Resolver: identifies the tenant from the request's subdomain.
	// "acme.localhost" -> TenantID("acme")
	subResolver := resolver.NewSubdomainResolver("localhost")

	// 3. Manager: assembles Resolver + Store. Panics at startup if either
	// is missing — a config mistake must fail fast, not at request time.
	manager := tenant.New(
		tenant.WithResolver(subResolver),
		tenant.WithStore(memStore),
	)

	// 4. Your application handler — completely unaware of tenant-core,
	// it just reads the tenant back from context.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		t := tenantctx.FromContext(r.Context())
		if t == nil {
			http.Error(w, "no tenant in context", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"tenant_id": string(t.ID)})
	})

	// 5. Middleware: resolves the tenant, injects it into the request
	// context, and rejects with 404 (before "next" ever runs) if it fails.
	handler := nethttp.Wrap(manager, mux)

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
```

Testez avec `curl`, en simulant le sous-domaine via le header `Host` :

```bash
curl -H "Host: acme.localhost" http://localhost:8080/whoami
# {"tenant_id":"acme"}

curl -H "Host: unknown.localhost" http://localhost:8080/whoami
# 404 tenant not found — the "unknown" tenant doesn't exist in the Store
```

## 3. Ajouter le cache (CachedStore)

`store.CachedStore` enveloppe n'importe quel `tenant.Store` — `MemoryStore`
ici, mais un vrai Store Postgres fonctionnerait à l'identique — et ajoute un
cache à TTL plus une déduplication (`singleflight`) des accès concurrents en
cache miss sur le même tenant. Recommandé dès que votre `Get()` fait un vrai
aller-retour I/O (une requête DB à chaque requête HTTP ne tient pas la charge).

```go
cachedStore := store.NewCachedStore(memStore, 30*time.Second)

manager := tenant.New(
	tenant.WithResolver(subResolver),
	tenant.WithStore(cachedStore), // was: memStore
)
```

C'est le seul changement nécessaire — `CachedStore` implémente lui-même
`tenant.Store`, donc `Manager` ne voit aucune différence. Point d'attention :
un changement d'état du tenant (désactivation, etc.) met jusqu'à `ttl` pour se
propager à travers ce cache. Pour les bannissements, qui doivent être
immédiats, voir `banchecker` et `eventbus` dans
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## 4. Ajouter les permissions (RBAC)

```go
authz := rbac.New()
authz.DefineRole("acme", "admin", []string{"users:read", "users:write"})
authz.DefineRole("globex", "viewer", []string{"users:read"})

mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
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
```

N'oubliez pas de peupler `Roles` sur vos tenants (`&tenant.Tenant{ID: "acme",
State: tenant.Active, Roles: []string{"admin"}}`) — sans ça, `Can` retournera
toujours `false`.

Rappel important : les rôles sont définis **par tenant** — `"admin"` chez
`acme` n'implique rien chez `globex`. Il n'existe pas d'espace de noms de
rôles global.

## 5. Limiter le débit (RateLimiter)

```go
// LimitFunc decides the requests-per-second quota per tenant. Here a
// flat 5 req/s for everyone — in practice, derive it from the tenant's
// plan (a field you'd add to your own Tenant metadata, or a lookup keyed
// by t.ID).
limiter := ratelimit.NewTenantRateLimiter(func(t *tenant.Tenant) rate.Limit {
	return rate.Limit(5)
}, 10) // burst

mux.HandleFunc("GET /limited", func(w http.ResponseWriter, r *http.Request) {
	t := tenantctx.FromContext(r.Context())
	if t == nil {
		http.Error(w, "no tenant in context", http.StatusInternalServerError)
		return
	}

	if !limiter.Allow(t) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	_, _ = w.Write([]byte("ok"))
})
```

`TenantRateLimiter` est en mémoire locale, par instance. Si vous tournez
plusieurs instances derrière un load balancer et voulez un quota partagé
entre elles, voir le sous-module
[`ratelimit/redis`](ratelimit/redis/go.mod) (détails dans
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)) — non couvert ici.

## 6. Administrer les tenants (Admin API)

```go
package main

import (
	"context"
	"log"
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/admin"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
	"github.com/sylvinhio676-ux/tenant-core/store"
)

func main() {
	memStore := store.NewMemoryStore()
	_ = memStore.Create(context.Background(), &tenant.Tenant{ID: "acme", State: tenant.Active})

	// eventbus.NewMemoryEventBus() is single-instance only — fine to get
	// started, but every server instance would keep its own view of who's
	// banned. For multi-instance propagation, see eventbus/redis.
	bus := eventbus.NewMemoryEventBus()

	adminService := admin.NewAdminService(memStore, bus)
	adminHandler := admin.NewHTTPHandler(adminService)

	log.Println("admin API listening on :9090")
	log.Fatal(http.ListenAndServe(":9090", adminHandler))
}
```

```bash
curl -X PATCH http://localhost:9090/tenants/acme/ban
# 204 No Content
```

**Important** : sans `admin.WithAuthenticator(...)`, cette API n'est **pas
protégée** — n'importe qui peut bannir/désactiver/réactiver n'importe quel
tenant. Configurez un `Authenticator` avant tout déploiement exposé :

```go
adminHandler := admin.NewHTTPHandler(
	adminService,
	admin.WithAuthenticator(myAuthenticator), // implements admin.Authenticator
)
```

Détails complets (JWT, API key, mTLS... tenant-core ne fournit aucune
implémentation concrète, c'est à votre application de la fournir) dans
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## 7. Et si vous utilisez Gin / Echo / Chi ?

Resolver et Store restent strictement identiques — seule la ligne de
middleware change.

**Gin** :

```bash
go get github.com/sylvinhio676-ux/tenant-core/middleware/gin@v0.1.0
```

```go
import ginmw "github.com/sylvinhio676-ux/tenant-core/middleware/gin"

r := gin.Default()
r.Use(ginmw.Middleware(manager))
```

**Echo** :

```bash
go get github.com/sylvinhio676-ux/tenant-core/middleware/echo@v0.1.0
```

```go
import echomw "github.com/sylvinhio676-ux/tenant-core/middleware/echo"

e := echo.New()
e.Use(echomw.Middleware(manager))
```

**Chi** :

```bash
go get github.com/sylvinhio676-ux/tenant-core/middleware/chi@v0.1.0
```

```go
import chimw "github.com/sylvinhio676-ux/tenant-core/middleware/chi"

r := chi.NewRouter()
r.Use(chimw.Middleware(manager))
```

## 8. Aller plus loin

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — comprendre l'architecture
  complète (rationale, concurrence, trade-offs, limites connues).
- [`eventbus/redis`](eventbus/redis) — propagation d'événements (bannissement,
  etc.) entre plusieurs instances via Redis Pub/Sub.
- [`metrics/prometheus`](metrics/prometheus) — observabilité (métriques
  tenant-scoped exposées au format Prometheus).
- [`cmd/server/main.go`](cmd/server/main.go) — exemple complet assemblant
  Resolver + CachedStore + RBAC + graceful shutdown + healthchecks.
- Le tag actuel est `v0.2.0` — l'API **n'est pas encore stabilisée en
  v1.0.0**. Consultez les notes de version avant toute mise à jour, une
  rupture de compatibilité reste possible avant le premier `v1.0.0`.
