# Getting started

🇬🇧 English · [🇫🇷 Français](GETTING_STARTED.fr.md)

A practical guide to integrating tenant-core into your own project, step by
step. Each tier works on its own — you can stop at any one of them and have
something functional. To understand the architectural decisions behind each
component (concurrency, trade-offs, guarantees), see
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — this document is "how-to"
only.

## 1. Installation

```bash
go get github.com/sylvinhio676-ux/tenant-core@v0.2.0
```

## 2. The bare minimum: resolving a tenant

Three building blocks are enough for an HTTP request to know "which tenant"
it belongs to:

- a `Store` — where your tenants are stored (`store.MemoryStore` here, to get
  started; in production this will be your own implementation of the
  `tenant.Store` interface, e.g. Postgres);
- a `Resolver` — how to identify the tenant from the request
  (`resolver.SubdomainResolver` here, based on the subdomain);
- a `Manager` — assembles the two, plus a middleware that uses it.

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

Test it with `curl`, simulating the subdomain via the `Host` header:

```bash
curl -H "Host: acme.localhost" http://localhost:8080/whoami
# {"tenant_id":"acme"}

curl -H "Host: unknown.localhost" http://localhost:8080/whoami
# 404 tenant not found — the "unknown" tenant doesn't exist in the Store
```

## 3. Adding a cache (CachedStore)

`store.CachedStore` wraps any `tenant.Store` — `MemoryStore` here, but a real
Postgres-backed Store works identically — and adds a TTL cache plus
deduplication (`singleflight`) of concurrent cache-miss calls for the same
tenant. Recommended as soon as your `Get()` does a real I/O round trip (a DB
query on every single HTTP request doesn't scale).

```go
cachedStore := store.NewCachedStore(memStore, 30*time.Second)

manager := tenant.New(
	tenant.WithResolver(subResolver),
	tenant.WithStore(cachedStore), // was: memStore
)
```

That's the only change needed — `CachedStore` implements `tenant.Store`
itself, so `Manager` sees no difference. One caveat: a tenant state change
(disabling, etc.) takes up to `ttl` to propagate through this cache. For bans,
which must be instant, see `banchecker` and `eventbus` in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## 4. Adding permissions (RBAC)

```go
authz := rbac.New()
authz.DefineRole("acme", "admin", "users:read", "users:write")
authz.DefineRole("globex", "viewer", "users:read")

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

Don't forget to populate `Roles` on your tenants (`&tenant.Tenant{ID: "acme",
State: tenant.Active, Roles: []tenant.Role{"admin"}}`) — without it, `Can` always
returns `false`.

Important reminder: roles are defined **per tenant** — `"admin"` at `acme`
implies nothing at `globex`. There is no global role namespace.

## 5. Rate limiting (RateLimiter)

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

`TenantRateLimiter` keeps its state in local process memory, per instance. If
you run multiple instances behind a load balancer and want a quota shared
across them, see the [`ratelimit/redis`](ratelimit/redis/go.mod) submodule
(details in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)) — not covered here.

## 6. Administering tenants (Admin API)

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

**Important**: without `admin.WithAuthenticator(...)`, this API is **not
protected** — anyone can ban/disable/reactivate any tenant. Configure an
`Authenticator` before any exposed deployment:

```go
adminHandler := admin.NewHTTPHandler(
	adminService,
	admin.WithAuthenticator(myAuthenticator), // implements admin.Authenticator
)
```

Full details (JWT, API key, mTLS... tenant-core provides no concrete
implementation — that's your application's responsibility to supply) in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## 7. What if you're using Gin / Echo / Chi?

Resolver and Store stay exactly the same — only the middleware line changes.

**Gin**:

```bash
go get github.com/sylvinhio676-ux/tenant-core/middleware/gin@v0.1.0
```

```go
import ginmw "github.com/sylvinhio676-ux/tenant-core/middleware/gin"

r := gin.Default()
r.Use(ginmw.Middleware(manager))
```

**Echo**:

```bash
go get github.com/sylvinhio676-ux/tenant-core/middleware/echo@v0.1.0
```

```go
import echomw "github.com/sylvinhio676-ux/tenant-core/middleware/echo"

e := echo.New()
e.Use(echomw.Middleware(manager))
```

**Chi**:

```bash
go get github.com/sylvinhio676-ux/tenant-core/middleware/chi@v0.1.0
```

```go
import chimw "github.com/sylvinhio676-ux/tenant-core/middleware/chi"

r := chi.NewRouter()
r.Use(chimw.Middleware(manager))
```

## 8. Going further

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — understand the full
  architecture (rationale, concurrency, trade-offs, known limitations).
- [`eventbus/redis`](eventbus/redis) — event propagation (bans, etc.) across
  multiple instances via Redis Pub/Sub.
- [`metrics/prometheus`](metrics/prometheus) — observability (tenant-scoped
  metrics exposed in Prometheus format).
- [`cmd/server/main.go`](cmd/server/main.go) — a complete example wiring up
  Resolver + CachedStore + RBAC + graceful shutdown + healthchecks.
- The current tag is `v0.2.0` — the API **is not yet stabilized at
  v1.0.0**. Check the release notes before upgrading; a breaking change
  remains possible before the first `v1.0.0`.
