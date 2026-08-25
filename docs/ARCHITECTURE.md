# tenant-core — Architecture and complete technical documentation

> Native Go multi-tenancy toolkit: resolution, context isolation, cache, real-time ban propagation, rate limiting, RBAC, metrics, Admin API, and multi-instance propagation — distributed as a middleware library compatible with existing Go routers.

---

## Table of contents

1. [Overview](#1-overview)
2. [Core principles](#2-core-principles)
3. [Architecture overview](#3-architecture-overview)
4. [Step 1 — Foundations](#4-step-1--foundations)
5. [Step 2 — Store and cache](#5-step-2--store-and-cache)
6. [Step 3 — Real-time ban](#6-step-3--real-time-ban)
7. [Step 4 — RateLimiter and CacheKeyer](#7-step-4--ratelimiter-and-cachekeyer)
8. [Step 5 — RBAC and Metrics](#8-step-5--rbac-and-metrics)
9. [Step 6 — Framework adapters](#9-step-6--framework-adapters)
10. [Step 7 — Admin API and Redis EventBus](#10-step-7--admin-api-and-redis-eventbus)
11. [Step 8 — Test helpers (tenanttest)](#11-step-8--test-helpers-tenanttest)
12. [Final complete architecture](#12-final-complete-architecture)
13. [Data flow](#13-data-flow)
14. [Concurrency and thread-safety](#14-concurrency-and-thread-safety)
15. [Testability](#15-testability)
16. [Limitations and future evolutions](#16-limitations-and-future-evolutions)
17. [Package tree](#17-package-tree)
18. [Decisions / points to clarify](#18-decisions--points-to-clarify)

---

## 1. Overview

### Goal of tenant-core

`tenant-core` is a Go toolkit whose goal is to solve a recurring problem in multi-company SaaS applications:

> When a request comes in, the application must always know which tenant it belongs to, and prevent one tenant's context from ever being mixed up with another's.

```text
                 YOUR APPLICATION
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
       Company A     Company B     Company C
          │            │            │
       Users A       Users B       Users C
       Data A        Data B        Data C
```

### Problem solved

Without a dedicated toolkit, every team reinvents its own multi-tenant handling: resolving the tenant from the request, manually propagating it (`handler(request, tenant)`, `service(request, tenant)`, `repository(request, tenant)`...), isolating the cache, quotas, permissions — with the constant risk of data leaking between tenants.

`tenant-core` addresses this with two fundamental operations:

1. **Tenant resolution** — from an HTTP request, determine which tenant it belongs to.
2. **Tenant context propagation** — pass this information to every layer of the application without any extra explicit parameter.

```text
GET https://company-a.example.com/users

tenant-core must understand:

This request
     ↓
belongs to
     ↓
Tenant A

Then propagate this information:

Request
   │
   ▼
TenantResolver
   │
   │ "It's Tenant A"
   ▼
ContextInjector
   │
   │ context = Tenant A
   ▼
Middleware
   │
   ▼
Handler
```

### Use cases

- A SaaS platform where each client (company) must see only its own data, and never access another's.
- A system where banning a tenant (fraud, abuse) must be applied immediately, across all server instances.
- An application deployed behind any Go router (`net/http`, Gin, Echo, Chi) without duplicating the multi-tenant logic for each one.
- A need for differentiated quotas and permissions per tenant, without a rigid global configuration.

### General philosophy

`tenant-core` does not try to reinvent an HTTP server, nor impose a data isolation strategy (shared `tenant_id`, separate schema, separate database). It positions itself as a toolkit that treats the **tenant as a first-class citizen** at every layer — resolution, cache, quotas, permissions, metrics — without ever imposing how the data itself is isolated at the storage layer.

### Why context.Context?

Rather than making the tenant travel as an explicit parameter through the entire application stack:

```go
handler(request, tenant)
service(request, tenant)
repository(request, tenant)
```

`tenant-core` relies on Go's standard `context.Context`:

```text
Request
   │
   ▼
context.Context
   │
   ├── Tenant A
   ├── deadline
   └── cancellation
```

The tenant thus becomes accessible to any component that needs it, without changing the signature of every intermediate function.

---

## 2. Core principles

These principles were consistently applied across the toolkit's eight construction steps.

### 2.1 Separation of concerns

Each component has a single, clearly delimited responsibility. The `Resolver` doesn't know about the `Store`; the `Store` doesn't know about the `EventBus`; the `EventBus` doesn't know about its consumers.

### 2.2 Minimal interfaces

> An interface should only expose what its consumer actually needs.

This is the principle that drove the separation between `tenant.Store` (read, consumed by `Manager`) and `tenant.AdminStore` (write, consumed by `admin.Service`) — rather than a single `Store` interface bloated with `Create`, `Update`, `Ban`, `Disable`, etc.

### 2.3 Go's structural typing

Go does not require a type to explicitly declare `implements InterfaceX`. As soon as a type has the methods an interface expects, it satisfies it automatically:

```text
             tenant.Store
                  ▲
       ┌──────────┼───────────┐
       │          │           │
       ▼          ▼           ▼
 MemoryStore CachedStore  DBStore (future)
```

This mechanism lets `SubdomainResolver`, `MemoryStore`, `CachedStore`, `RedisEventBus`, `MemoryMetrics`, etc. satisfy the contracts defined in the `tenant` package without ever needing to import that package in reverse — avoiding circular dependencies.

### 2.4 Agnosticism

The toolkit's core does not depend on any particular infrastructure technology:

- not an HTTP framework (Gin, Echo, Chi);
- not Redis;
- not Prometheus;
- not any particular database engine.

```text
             tenant
               │
       ┌───────┴────────┐
       │                │
   Interface       Interface
       │                │
       ▼                ▼
Implementation A   Implementation B
```

### 2.5 Fail-fast

A **program configuration** error (a required component missing, an unreachable Redis connection at startup) must be caught **immediately**, generally via a `panic` or an error returned at startup — rather than discovered silently in production. An error occurring during **request processing**, on the other hand, is always handled via the standard `error` mechanism.

### 2.6 Testability

Each component is designed to be tested independently, with no dependency on real infrastructure (database, Redis, a full HTTP framework). The `tenanttest` package extends this principle for external users of the toolkit.

### 2.7 Multi-tenant isolation

> Any shared resource must be explicitly scoped by `TenantID`.

This principle applies transversally: to storage (`TenantID → Tenant`), to the cache (`TenantID → Cache Key`), to rate limiting (`TenantID → Rate Limit Bucket`), to permissions (`TenantID → Roles → Permissions`).

### 2.8 Safe concurrency

The toolkit is meant for HTTP applications that naturally handle concurrent requests. Every component with shared state is protected by the synchronization mechanism suited to its access profile (see [section 14](#14-concurrency-and-thread-safety)).

### 2.9 Observability

The toolkit allows measuring its own behavior (requests, errors, latency, RBAC denials, rate-limit denials) without imposing a particular metrics backend.

### 2.10 Separation of business logic and transport

The business logic (`Manager`, `admin.Service`, `RBAC`) never knows which transport protocol invokes it (HTTP, CLI, future gRPC, tests). That is the role of the adapters, which bridge the two.

---

## 3. Architecture overview

### The central principle

> From an HTTP request, identify the tenant, retrieve its state, then apply the various protection and isolation mechanisms.

```text
                    HTTP REQUEST
                         │
                         ▼
               ┌──────────────────┐
               │    Resolver      │
               │                  │
               │ Request → ID     │
               └────────┬─────────┘
                        │
                  TenantID
                        │
                        ▼
               ┌──────────────────┐
               │      Store       │
               │                  │
               │ ID → *Tenant     │
               └────────┬─────────┘
                        │
                    *Tenant
                        │
                        ▼
               ┌──────────────────┐
               │   ContextInjector│
               │    tenantctx     │
               │                  │
               │ *Tenant → Context│
               └────────┬─────────┘
                        │
                        ▼
                   CONTEXT
```

### The fundamental package separation

```text
tenant
│
├── defines the contracts and business model (the "what")
│
├── tenantctx        → carries the tenant via context.Context
├── resolver         → concrete resolution (the "how")
├── store            → persistence and cache
├── eventbus         → event propagation
├── ratelimit        → per-tenant quotas
├── cachekey         → cache key isolation
├── rbac             → per-tenant permissions
├── metrics          → observability
├── middleware       → router adapters
│   ├── net/http
│   ├── gin
│   ├── echo
│   └── chi
├── admin            → tenant administration
├── eventbus/redis   → multi-instance propagation
└── tenanttest       → test tools
```

> **The `tenant` package defines the "what"; the sub-packages define the "how".**

This organization enables:

- **low coupling** — each sub-package only depends on the contract it implements, never on other implementations;
- **minimal interfaces** — each component only exposes what its consumer needs;
- **framework agnosticism** — the core knows nothing of Gin, Echo, Chi, Redis, Prometheus;
- **testability** — each contract can be satisfied by a fake implementation in tests;
- **extensibility** — a new implementation (`PostgresStore`, `RedisRateLimiter`, `PrometheusMetrics`) can be added without modifying the core;
- **swappable implementations** — moving from `MemoryEventBus` to `RedisEventBus` changes no contract, only the adapter used.

---

## 4. Step 1 — Foundations

### 4.1 Goal

Before Redis, before the Gin/Echo/Chi middlewares, before the Admin API or RBAC, a fundamental question needed answering:

> How is an HTTP request associated with a tenant, and how do we guarantee that tenant's data stays isolated?

```text
                    HTTP Request
                         │
                         ▼
                ┌─────────────────┐
                │    Resolver     │
                │ "Which tenant?" │
                └────────┬────────┘
                         │
                    TenantID
                         │
                         ▼
                ┌─────────────────┐
                │ Tenant Context  │
                │ "Which tenant   │
                │ for this        │
                │ request?"       │
                └────────┬────────┘
                         │
                         ▼
                  Business handler
                         │
                         ▼
                 tenantctx.FromContext()
                         │
                         ▼
                    *Tenant
```

### 4.2 The fundamental types

**`TenantID`**

```go
type TenantID string
```

A dedicated named type rather than a plain `string`, to express intent and benefit from type safety: `var id tenant.TenantID` is conceptually different from `var email string`. The compiler refuses to mix up the two, even though both are "just strings" internally.

**`State`**

```go
type State string

const (
    Active   State = "active"
    Disabled State = "disabled"
    Banned   State = "banned"
)
```

The three states have distinct business meanings:

- **`Active`** — the tenant can access the system normally.
- **`Disabled`** — the tenant is disabled (e.g. subscription ended). Disabling can be propagated with a slight delay, notably via a cache (*eventual* consistency).
- **`Banned`** — the tenant is banned for fraud or abuse. Unlike `Disabled`, a ban must be propagated **immediately** — which justifies the later introduction of `BanChecker` and the `EventBus` (Step 3).

**`Tenant`**

```go
type Tenant struct {
    ID    TenantID
    State State
    Roles []string
}
```

```text
Tenant
 ├── ID
 ├── State
 └── Roles
```

The `Roles` field was planned from the start to allow the later integration of RBAC (Step 5).

### 4.3 The Resolver contract

An important architectural decision: the contract lives in the root `tenant` package, not in the `resolver` package.

```go
type Resolver interface {
    Resolve(r *http.Request) (TenantID, error)
}
```

This interface answers a single question: *which tenant does this HTTP request belong to?* It says nothing about **how** the tenant is found.

```text
tenant
   │
   └── defines the contract
          │
          ▼
      Resolver

resolver/
   └── SubdomainResolver (implementation)
```

### 4.4 SubdomainResolver

The first concrete implementation, based on the request's subdomain:

```text
tenant-a.example.com
        │
        ▼
SubdomainResolver
        │
        ▼
TenantID("tenant-a")
```

```text
https://school-a.example.com/users
                   │
                   ▼
              tenant-a
```

### 4.5 Why SubdomainResolver is not in `tenant`

```text
tenant
 │
 └── Resolver
       ▲
       │
       │ automatically satisfied by
       │
resolver
 │
 └── SubdomainResolver
```

Thanks to Go's structural typing, `SubdomainResolver` never needs to write `implements tenant.Resolver`. It just needs to have the method `Resolve(*http.Request) (tenant.TenantID, error)`.

### 4.6 The Context Injector — `tenantctx`

Once the tenant is identified, it needs to be passed to the following layers. The `tenantctx/` package handles storing the tenant in the standard `context.Context`:

```go
ctx := tenantctx.WithTenant(ctx, tenant)
```

```text
context.Context
       │
       └── Tenant
            ├── ID
            ├── State
            └── Roles
```

Business code then retrieves the tenant with:

```go
t := tenantctx.FromContext(ctx)
```

A business function therefore doesn't need to know about the subdomain, HTTP, Gin, Echo, Chi, Redis, or how the tenant was resolved — it simply receives a `context.Context`.

### 4.7 Why context.Context

The context lets the tenant's identity travel through the layers, without needing to add `tenantID string` to every signature:

```text
HTTP
 │
 ▼
Middleware
 │
 ▼
Service
 │
 ▼
Repository
 │
 ▼
Database
```

### 4.8 Isolation between tenants

It wasn't enough to be able to identify a tenant: it also had to be guaranteed that tenant A's request context could never accidentally be reused for tenant B.

```text
Request A
tenant-a.example.com
       │
       ▼
Context A
TenantID = tenant-a

Request B
tenant-b.example.com
       │
       ▼
Context B
TenantID = tenant-b
```

The two contexts must remain completely independent.

### 4.9 The critical isolation test

Isolation was treated as a property to be tested **explicitly**, never simply assumed.

```text
Create context A
      │
      ▼
inject tenant-A
      │
      ▼
Create context B
      │
      ▼
inject tenant-B
      │
      ▼
verify A == tenant-A
verify B == tenant-B
```

The goal in particular is to detect a bad implementation that would use a global variable instead of `context.Context`:

```go
var currentTenant *Tenant // ❌ dangerous
```

```text
Goroutine A
tenant-A
     │
     ▼
globalTenant = A

Goroutine B
tenant-B
     │
     ▼
globalTenant = B

Goroutine A
     │
     ▼
gets B back ❌
```

The standard context, on the other hand, is **immutable** — `WithTenant()` never modifies an existing context, it creates a new one that wraps it. Two contexts created from different branches can never step on each other. This mechanism precisely avoids this kind of dangerous implicit sharing.

**Two concrete tests validated this property**:

- A **structural** test: inject two tenants into two separate contexts, verify they remain distinct, and that mutating the tenant retrieved from one context never affects the other context.
- A test under **real concurrency**: a hundred goroutines simulating simultaneous requests alternating between two tenants, systematically run with `go test -race`, to guarantee that no goroutine ever sees another's tenant.

### 4.10 The private context key

An important technical detail: the key used by `context.WithValue` to store the tenant is **never** a plain `string`. A `string` key like `"tenant"` could collide with any other third-party library using the same key, with a real risk of silent overwriting.

The solution chosen is a **private, unexported** key type:

```go
type contextKey int

const tenantContextKey contextKey = 0
```

Since `contextKey` is an unexported type, no other package can create a value of that type — even knowing its name. And even if another package also defined a `type contextKey int` with the value `0`, it would be a **different** Go type (types are compared by full package + name identity), so `context.WithValue` would never confuse them. This is the pattern officially documented by the Go standard library itself.

### 4.11 Package architecture after Step 1

```text
tenant-core/
│
├── tenant.go
│   ├── TenantID
│   ├── State
│   ├── Tenant
│   └── Resolver
│
├── tenantctx/
│   └── tenant context
│
└── resolver/
    └── SubdomainResolver
```

| Package | Responsibility |
|---|---|
| `tenant` | Fundamental concepts and contracts |
| `tenantctx` | Carries the tenant via `context.Context` |
| `resolver` | Concrete tenant resolution |
| `SubdomainResolver` | Identification from the subdomain |

### 4.12 The architectural principle established as of Step 1

```text
                CONTRACTS
                   │
                   ▼
               tenant package
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
    resolver     store     eventbus
        │          │          │
        ▼          ▼          ▼
 implementation implementation implementation
```

This principle recurred constantly throughout the rest of the toolkit: `tenant.Resolver` ← `SubdomainResolver`, `tenant.Store` ← `MemoryStore`/`CachedStore`, `eventbus.EventBus` ← `MemoryEventBus`/`RedisEventBus`.

**Step summary**: identify → represent → carry → isolate the tenant. This foundation made it possible to stay agnostic of frameworks and to build the Gin/Echo/Chi adapters without ever modifying the core of the system.

---

## 5. Step 2 — Store and cache

### 5.1 Goal

Step 1 answered *"which tenant does this request correspond to?"*, but only with its identifier. Now the question was: *"what is this tenant's information, and what state is it in?"* — that is the `Store`'s role.

```text
                    HTTP Request
                         │
                         ▼
                   ┌──────────┐
                   │ Resolver │
                   └────┬─────┘
                        │
                     TenantID
                        │
                        ▼
                 ┌─────────────┐
                 │ CachedStore │
                 └──────┬──────┘
                        │
                 cache hit?
                  /           \
                yes            no
                 │              │
                 │              ▼
                 │        ┌────────────┐
                 │        │ MemoryStore│
                 │        └──────┬─────┘
                 │               │
                 └───────┬───────┘
                         ▼
                      *Tenant
```

### 5.2 The `tenant.Store` contract

```go
type Store interface {
    Get(ctx context.Context, id TenantID) (*Tenant, error)
    IsBanned(ctx context.Context, id TenantID) (bool, error)
}
```

- **`Get`** — retrieves a full tenant.
- **`IsBanned`** — a specialized, fast check for a ban, which becomes especially important with `BanChecker` (Step 3).

This separation matters: the normal resolution path has no need to know about administration operations (see Step 7, `AdminStore`).

### 5.3 Why `Store` is an interface

```text
             tenant.Store
                  ▲
       ┌──────────┼───────────┐
       │          │           │
       ▼          ▼           ▼
 MemoryStore CachedStore  DBStore (future)
```

The toolkit's core must be able to swap `MemoryStore` for `PostgreSQLStore`, `MySQLStore`, `RedisStore`, or `APIStore`, without ever modifying `Manager`.

### 5.4 MemoryStore

```go
type MemoryStore struct {
    mu      sync.RWMutex
    tenants map[tenant.TenantID]*tenant.Tenant
}
```

```text
MemoryStore
│
├── mu
│   └── RWMutex
│
└── tenants
    └── map[TenantID]*Tenant
```

### 5.5 Why `sync.RWMutex`

The store is accessed simultaneously by multiple HTTP goroutines:

```text
Request A ──────┐
Request B ──────┤
Request C ──────┼──► MemoryStore
Request D ──────┤
Request E ──────┘
```

A plain Go map isn't safe for concurrent access involving writes. `RWMutex` allows two kinds of locking:

```text
Reader A ── RLock ──►
Reader B ── RLock ──►
Reader C ── RLock ──►

Writer
   │
   ▼
 Lock()
   │
   ▼
 modification
   │
   ▼
Unlock()
```

Several readers can read at the same time; a write remains exclusive. This profile fits a `Store` particularly well, since reads are far more frequent than writes.

### 5.6 The shared-pointer trap

An important subtlety encountered during design: the map holds `map[TenantID]*Tenant`, i.e. **pointers**, not copies.

```text
Map
 │
 └── *Tenant ──────────┐
                       │
                       ▼
                    Tenant
                    State
```

If `Get()` returns `t` directly (the internal pointer), the caller gets direct access to the object actually stored in the store. Doing `t.State = tenant.Disabled` outside the lock, while another goroutine reads that same field via `Get()`, causes a genuine **data race** — detectable by `go test -race`.

```text
Goroutine A                 Goroutine B

t.State = Banned
       │
       │                 Get()
       │                   │
       ▼                   ▼
   write                 read
```

> **Protecting only the map is not enough when the map's values are mutable pointers.**

**The solution adopted**:

- **`Get()` always returns a copy**, never the internal pointer. The external consumer can therefore never mutate the store's internal state via the pointer it received.
- Write operations (`SetState`, `Create`, `Update`) modify the internal object **directly, under an exclusive lock (`Lock`)** — never via a Get + modify + write-back round trip, which would recreate a *lost update* window.

```text
MemoryStore
    │
    ▼
internal *Tenant
    │
    │ copy
    ▼
returned *Tenant
```

An **internal write primitive** (`set`, unexported) is still used internally by `Create`/`Update`/`SetState`, but is never exposed publicly — the public write contract goes exclusively through these three explicit methods, never through a raw write.

### 5.7 `Get()`

```text
TenantID
   │
   ▼
MemoryStore
   │
   ├── look up the tenant
   │
   ├── check it exists
   │
   └── return the tenant (copy)
```

If the tenant doesn't exist, an explicit sentinel error is returned: `ErrTenantNotFound`. This lets upper layers distinguish a tenant that genuinely doesn't exist from some other technical error.

### 5.8 `IsBanned()`

```text
TenantID
   │
   ▼
Store
   │
   ▼
State == Banned?
   │
   ├── yes → true
   └── no → false
```

### 5.9 State change — `Disable()` / `SetState()`

A tenant can move from `Active` to `Disabled` (e.g. end of subscription):

```text
subscription ended
       │
       ▼
Disable()
       │
       ▼
State = Disabled
```

This change is protected by the same synchronization mechanism as other writes:

```text
Disable()
   │
   ▼
Lock()
   │
   ▼
modify tenant
   │
   ▼
Unlock()
```

### 5.10 Why a TTL is needed

A remote database can be much slower than an in-memory read. Without a cache:

```text
Request
   │
   ▼
Store
   │
   ▼
Database
   │
   ▼
Tenant
```

If thousands of requests continually ask for the same tenant, this becomes expensive. Hence the introduction of a cache in front of the store.

### 5.11 CachedStore

```text
store/
├── memory.go
└── cached.go
```

`CachedStore` is not a replacement for `Store`: it **wraps** it.

```text
CachedStore
     │
     └── source Store
             │
             ▼
        MemoryStore
```

The `source` field is based on the `tenant.Store` **interface**, never on a concrete implementation — an important decision: the cache doesn't depend on any particular implementation, letting it wrap any future `Store` (Postgres, Redis, etc.) with no modification.

### 5.12 How the cache works

**Cache HIT**

```text
Get("tenant-a")
       │
       ▼
   Cache found
       │
       ▼
   still valid?
       │
       ▼
     Tenant
```

**Cache MISS**

```text
Get("tenant-a")
       │
       ▼
   Cache absent/expired
       │
       ▼
 source.Get(...)
       │
       ▼
    Tenant
       │
       ▼
 store in cache
       │
       ▼
    return
```

### 5.13 The TTL

Each cache entry has a validity duration (e.g. 30 seconds). After expiration, the entry is considered invalid and the underlying store is queried again.

```text
Cache
 │
 ├── tenant-a
 │      expired ❌
 │
 ▼
source.Get()
```

### 5.14 Why accepting slight inconsistency is fine for `Disabled`

The TTL is particularly well suited to the `Disabled` state: during the cache's validity window, an instance might still consider a disabled tenant active. This is a **accepted** temporary inconsistency.

```text
Disabled  → eventual propagation (TTL acceptable)
Banned    → immediate propagation (requires an event — Step 3)
```

This distinction shows up in the property of `MemoryStore.IsBanned()`: unlike a regular `Get()`, `IsBanned` (and later, in `CachedStore`, its equivalent) systematically bypasses the cache to query the source of truth directly.

### 5.15 Protection against duplicate calls — `singleflight`

An efficiency problem (not a safety one) remains despite the `RWMutex`: if 500 concurrent requests for the same tenant arrive at the exact moment of a cache miss, they can all simultaneously observe the entry's absence before any of them has had time to fill it — causing 500 duplicate calls to the source of truth (a phenomenon known as *cache stampede* or *thundering herd*).

The solution adopted is `golang.org/x/sync/singleflight`, which guarantees that only one real call goes out to the source for a given key, with concurrent callers waiting and receiving the same result:

```go
v, err, _ := cs.group.Do(string(id), func() (interface{}, error) {
    t, err := cs.source.Get(ctx, id)
    // ...
    return t, nil
})
```

### 5.16 Complete architecture of Step 2

```text
tenant-core/
│
├── tenant.go
│   │
│   ├── Tenant
│   └── Store
│
└── store/
    │
    ├── memory.go
    │   └── MemoryStore
    │
    └── cached.go
        └── CachedStore
```

```text
                  tenant.Store
                       ▲
                       │
              ┌────────┴────────┐
              │                 │
        MemoryStore        CachedStore
                                │
                                │ source
                                ▼
                           tenant.Store
```

Typical configuration:

```text
                 Manager
                    │
                    ▼
              CachedStore
                    │
                    ▼
              MemoryStore
```

### 5.17 Step 2 summary

| Element | Responsibility |
|---|---|
| `tenant.Store` | Read contract for tenants |
| `MemoryStore` | In-memory storage |
| `RWMutex` | Protection of concurrent access |
| `Get()` | Retrieve a tenant (copy, never the internal pointer) |
| `IsBanned()` | Specialized ban check |
| `Disable()` / `SetState()` | State change, atomic under `Lock()` |
| `CachedStore` | Adds a cache in front of a `Store` |
| `TTL` | Cache entry expiration |
| `singleflight` | Deduplication of concurrent calls on a cache miss |
| `source Store` | Decouples the cache from the concrete implementation |
| `ErrTenantNotFound` | Explicit identification of a non-existent tenant |

> **Step 1 made it possible to identify the tenant; Step 2 makes it possible to retrieve its state safely and efficiently, while preparing for concurrency and caching concerns.**

---

## 6. Step 3 — Real-time ban

### 6.1 Goal

Step 2's cache was deliberately *eventual-consistent* for disabling. But for a ban due to fraud or abuse, this behavior is not acceptable:

```text
Instance A
    │
    ▼
tenant-A = Banned

Instance B (cache not expired)
    │
    ▼
tenant-A = Active   ❌
```

The goal of this step is to introduce an `EventBus`, a `MemoryEventBus`, a `BanChecker`, and the rule that `Ban()` is **synchronous**.

```text
              BAN
               │
               ▼
        state change
               │
               ▼
        publish event
               │
        ┌──────┴──────┐
        ▼             ▼
    Instance A     Instance B
        │             │
        ▼             ▼
   BanChecker     BanChecker
        │             │
        ▼             ▼
   immediate blocking
```

### 6.2 Why TTL alone is not enough

```text
TTL = 30 seconds

12:00:00 → tenant-A = Active
12:00:05 → Admin bans tenant-A

Another instance still holds:
tenant-A = Active (expires 12:00:30)
```

Without an additional mechanism, this instance could accept the tenant until 12:00:30. Acceptable for `Disabled`, unacceptable for `Banned`.

```text
Disabled → eventual consistency → TTL acceptable
Banned   → near-immediate consistency → event required
```

### 6.3 `TenantEvent`

```go
type TenantEvent struct {
    TenantID  tenant.TenantID
    State     tenant.State
    Timestamp time.Time
}
```

```text
TenantEvent
├── TenantID
├── State
└── Timestamp
```

`TenantID` and `State` are the strict functional minimum for a subscriber to know what to do. `Timestamp` was added deliberately: without it, a future component (audit, logging) couldn't even answer *"when did this change happen?"* — and, more importantly, it becomes indispensable for solving a temporal consistency issue (see 6.9).

The event doesn't say how the change should be handled — it simply says: *"tenant tenant-A is now in the Banned state."*

### 6.4 The `EventBus` interface

```go
type EventBus interface {
    Publish(ctx context.Context, event TenantEvent) error
    Subscribe(handler func(TenantEvent)) error
}
```

```text
Publish
   │
   ▼
send an event

Subscribe
   │
   ▼
receive events
```

### 6.5 Why `EventBus` is an interface

```text
                 EventBus
                    ▲
                    │
          ┌─────────┴─────────┐
          │                   │
          ▼                   ▼
 MemoryEventBus          RedisEventBus
```

The toolkit's core should only know about `eventbus.EventBus` — not Redis, NATS, Kafka, or RabbitMQ (possible future implementations). Same principle as `tenant.Store`.

### 6.6 MemoryEventBus

A fully in-memory implementation, used to develop the mechanism, test it, and avoid needing Redis during the early steps.

```text
MemoryEventBus
      │
      ├── subscribers (handlers)
      │
      └── Publish()
```

```text
Publish(TenantEvent)
       │
       ▼
MemoryEventBus
       │
       ├───────────────┐
       ▼               ▼
 Subscriber A      Subscriber B
       │               │
       ▼               ▼
    handler()        handler()
```

### 6.7 Isolating handlers by goroutine

An important implementation detail: **never** run handlers sequentially in the same goroutine. A bad approach would be:

```go
for _, handler := range handlers {
    handler(event) // ❌ a slow handler blocks all the following ones
}
```

If a handler is slow (`time.Sleep`) or panics, all following ones are delayed or never run. The principle adopted:

```text
Publish
  │
  ├──► goroutine Handler A
  │
  ├──► goroutine Handler B
  │
  └──► goroutine Handler C
```

Each handler is isolated and starts in parallel with the others.

**A second efficiency problem was identified**: the first version of `Publish()` held an `RLock()` for the entire duration of the handlers' execution, blocking any concurrent call to `Subscribe()`. The fix adopted: copy the handler list under `RLock`, release the lock immediately, then launch the goroutines from the copy — `Subscribe()` therefore never waits for in-flight handlers anymore.

### 6.8 Protection against panics — `recover()`

A user-supplied handler must never be able to bring down the whole process with a simple `panic(...)`. Each handler is therefore run with a recovery mechanism:

```go
defer func() {
    if r := recover(); r != nil {
        // log
    }
}()
```

> **A failing handler must never prevent other handlers from receiving the event.**

Crucial point for a toolkit meant for external applications: `recover()` only works **within the same goroutine** as the `panic()` — it must therefore be placed inside the function launched by `go`, never around the call to `Publish()` itself (which has already returned long before the handler actually runs).

**An accepted trade-off**: since each handler runs in its own goroutine, `Publish()` can no longer report handler errors directly to the caller. `Publish()` returning `nil` therefore means *"I successfully started broadcasting to the handlers"*, not *"all handlers processed the event successfully"*.

### 6.9 The BanChecker

The EventBus carries the event, but a component is needed that **reacts** to the ban.

```text
EventBus
   │
   │ TenantEvent{State: Banned}
   ▼
BanChecker
   │
   ▼
update its local state
```

```text
BanChecker
    │
    └── banned
         ├── tenant-A
         ├── tenant-C
         └── tenant-F
```

### 6.10 Why BanChecker exists in addition to the Store

The `Store` remains the source of truth. `BanChecker` answers a much more specialized question: *"is this tenant currently banned?"*, with an extreme speed requirement.

```text
Request
   ↓
IsBanned(tenant-A)
   ↓
RAM (BanChecker)
   ↓
true/false
```

If `IsBanned()` had to systematically call the source of truth, 10,000 requests for the same tenant would produce 10,000 accesses to the source. With `BanChecker`, that becomes 10,000 RAM reads — the source is only queried when a state change needs to be propagated (**push model**, as opposed to a pull model):

```text
Source
   │
   │ "tenant-A is now Banned"
   ▼
EventBus
   │
   ▼
BanChecker
   │
   ▼
tenant-A → true
```

### 6.11 The ban-priority principle

`Banned` must take priority over the normal cache. For example, if `CachedStore` still shows `Active` while `BanChecker` already knows `Banned`, the system must treat the tenant as banned. `BanChecker` becomes a kind of security barrier placed in front of the normal path:

```text
                 HTTP Request
                      │
                      ▼
                  Resolver
                      │
                      ▼
                   TenantID
                      │
                      ▼
                BanChecker
                      │
                ┌─────┴─────┐
                │           │
             Banned       Not banned
                │           │
                ▼           ▼
             Reject      CachedStore
                            │
                            ▼
                          Tenant
```

### 6.12 Conflict resolution by timestamp — causal ordering

A deeper consistency problem was identified: loading an **initial snapshot** at instance startup (necessary in a multi-instance environment, to know the state of past bans before subscribing) can conflict with a **recent event** received in the meantime.

**Problem scenario**: a tenant is unbanned (`Active`) just before a stale snapshot (started before the unban, but whose write arrives after the event) overwrites this information with `Banned` — the in-memory data would then become incorrect again.

**The solution adopted**: each `BanChecker` entry (not just a `banned` boolean) is associated with a **last-updated timestamp**. A write is only applied if its timestamp is **more recent** (or equal) than the one already stored — guaranteeing that stale information can never overwrite fresher information, regardless of the actual goroutine execution order.

**Rule also established**: `Subscribe()` must always be called **before** loading the initial snapshot, never the other way around — otherwise an event published in between could be missed (never received by any mechanism).

### 6.13 Synchronous Ban()

An essential distinction between synchronous and asynchronous:

**Synchronous (adopted)**

```text
Ban()
 │
 ├── state change
 │
 ├── publish event
 │
 └── return
```

The function doesn't return until the operations it guarantees have been carried out.

**Asynchronous (rejected)**

```text
Ban()
 │
 └── start goroutine
          │
          └── publish later

Ban() returns immediately
```

The problem with an asynchronous version: the caller would never know whether the ban was actually published.

### 6.14 Why Ban() must be synchronous

```go
err := Ban(ctx, tenantID)
```

`err == nil` must mean *"the ban operation was carried out successfully according to this layer's guarantees"*, and `err != nil` must mean the operation could not be properly carried out — letting the caller react immediately (e.g. by reporting the failure).

### 6.15 The Ban() flow

```text
Ban(tenant-A)
      │
      ▼
change state
      │
      ▼
Tenant = Banned
      │
      ▼
Publish(TenantEvent)
      │
      ▼
return
```

### 6.16 The non-atomicity problem (identified as early as this step)

`SetState()` followed by `Publish()` is **not** an atomic transaction. Two problematic scenarios:

```text
SetState() → SUCCESS
Publish()  → FAILURE
```

The source of truth says `Banned`, but the `EventBus` didn't transmit any event — other instances may not immediately know the tenant is banned.

```text
Publish()  → SUCCESS
SetState() → FAILURE
```

Even more dangerous: other instances would believe the tenant is banned, while the source of truth still says `Active` — a **lying event**.

**Decision adopted**: `SetState → Publish` (never the reverse), with the idea that a more robust mechanism (Outbox) could later solve durability and atomicity (see Step 7, section 10.8, and [future limitations](#16-limitations-and-future-evolutions)).

### 6.17 Why the Outbox wasn't needed at this step

> Build the correct contract and behavior first, then progressively reinforce reliability.

The foundations built here (`TenantEvent`, `EventBus`, `MemoryEventBus`, `BanChecker`, `Ban()`) later made it possible, in Step 7, to introduce `RedisEventBus` as a simple **adapter swap** — not a rewrite of the business core.

### 6.18 Complete architecture of Step 3

```text
tenant-core/
│
├── tenant.go
│   ├── Tenant
│   ├── State
│   ├── TenantID
│   └── Store
│
├── eventbus/
│   ├── event.go
│   │   └── TenantEvent
│   │
│   ├── eventbus.go
│   │   └── EventBus
│   │
│   └── memory.go
│       └── MemoryEventBus
│
└── banchecker/
    └── BanChecker
```

```text
                  tenant
                    │
                    │ TenantID / State
                    ▼
               TenantEvent
                    │
                    ▼
                EventBus
                    │
                    ▼
             MemoryEventBus
                    │
                    ▼
               BanChecker
```

### 6.19 Distributed architecture already prepared

```text
Today
              Instance A
                  │
             MemoryEventBus
                  │
                  ▼
             BanChecker A

Tomorrow (with Redis)
Instance A                         Instance B
    │                                  │
    ▼                                  ▼
Ban()                            BanChecker
    │                                  ▲
    ▼                                  │
RedisEventBus ─────── Redis ───────────┘
```

The `EventBus` contract never changes — only the implementation moves from `MemoryEventBus` to `RedisEventBus`.

### 6.20 Step 3 summary

| Element | Responsibility |
|---|---|
| `TenantEvent` | Represents a state change |
| `EventBus` | Publish/subscribe contract |
| `MemoryEventBus` | Local, in-memory `EventBus` |
| `Publish()` | Broadcasts an event |
| `Subscribe()` | Registers a handler |
| One goroutine per handler | Isolation and non-blocking between handlers |
| `recover()` | Prevents a panic from killing processing |
| `BanChecker` | Maintains immediate knowledge of banned tenants |
| `Ban()` | Triggers the ban change and its propagation, synchronously |
| Timestamp-based resolution | Prevents a stale snapshot from overwriting a more recent event |
| TTL | Always acceptable for `Disabled`, but insufficient for `Banned` |
| Redis | Will be the future distributed implementation (Step 7) |

> **Step 2 accepted delayed propagation via TTL; Step 3 introduces an event channel that turns a ban into an active, immediately propagated piece of information.**

Core architectural principle established:

- **Store** = "What is the truth about the tenant?"
- **Cache** = "How do we avoid re-reading that truth too often?"
- **EventBus** = "How do we announce that it just changed?"
- **BanChecker** = "How do we immediately enforce the critical ban rule?"

---

## 7. Step 4 — RateLimiter and CacheKeyer

### 7.1 Goal

This step adds two cross-cutting mechanisms that strengthen the toolkit without making it depend on any particular technology, keeping the principle set since Step 1: *the `tenant` package defines the contracts, the sub-packages provide the implementations.*

Two protections were still missing:

**Protection #1 — request abuse.** A tenant could send a disproportionate volume of requests (`tenant-A → 10,000 requests/second`) and monopolize server resources.

**Protection #2 — cache isolation.** A naive application cache key like `"user:123"` could cause a collision between two tenants each having a user with ID `123`:

```text
tenant-A + user-123
tenant-B + user-123
```

A global `user:123` key would create a data leak between tenants — exactly the kind of catastrophic bug a multi-tenant system must structurally prevent.

### 7.2 General architecture

```text
                    ┌──────────────────────┐
                    │     Application      │
                    └──────────┬───────────┘
                               │
                               ▼
                     ┌───────────────────┐
                     │ Tenant Middleware │
                     └─────────┬─────────┘
                               │
                               ▼
                       ┌──────────────┐
                       │    Tenant    │
                       │   Context    │
                       └──────┬───────┘
                              │
              ┌───────────────┴────────────────┐
              │                                │
              ▼                                ▼
      ┌───────────────┐                ┌───────────────┐
      │ RateLimiter   │                │  CacheKeyer   │
      └───────┬───────┘                └───────┬───────┘
              │                                │
              ▼                                ▼
       Per-tenant limit                 Isolated key
              │                                │
              └───────────────┬────────────────┘
                              │
                              ▼
                       Business application
```

`RateLimiter` and `CacheKeyer` are two independent components: neither should know Redis directly, which then allows for different implementations (`MemoryRateLimiter`, a future `RedisRateLimiter`; `DefaultCacheKeyer`).

### 7.3 RateLimiter — responsibility

`RateLimiter` answers one very simple question: *"is this tenant still allowed to make this request?"* It decides neither which tenant is used, nor how it's resolved or stored, nor how to respond in HTTP — it focuses solely on limiting.

### 7.4 Why RateLimiter is tied to the tenant

A naive global limit would penalize tenants against each other:

```text
Tenant A ─┐
Tenant B  ├──► same counter (❌ bad)
Tenant C ─┘
```

The correct approach isolates each counter per tenant:

```text
Tenant A → counter A → 100 req/min
Tenant B → counter B → 100 req/min
Tenant C → counter C → 100 req/min
```

The rate limiting's logical key is therefore the `TenantID`.

### 7.5 How it works — example

With a limit of 5 requests/minute for `tenant-A`:

```text
Request 1 → ALLOW
Request 2 → ALLOW
Request 3 → ALLOW
Request 4 → ALLOW
Request 5 → ALLOW
Request 6 → DENY
```

`tenant-B`, with its own independent counter (`0 / 5` used), remains allowed.

### 7.6 In-memory implementation

```text
MemoryRateLimiter
       │
       ▼
┌───────────────────────────┐
│ map[TenantID]*bucket      │
├───────────────────────────┤
│ tenant-A → counter        │
│ tenant-B → counter        │
│ tenant-C → counter        │
└───────────────────────────┘
```

Since multiple HTTP goroutines can access this structure simultaneously, its shared state must be protected — the same principle as with `MemoryStore`.

The concrete implementation adopted relies on a key (`TenantID`) associated with an individual *token bucket*-style limiter, each tenant having its own "bucket of tokens" (see [section 14](#14-concurrency-and-thread-safety) for concurrency details, notably the use of `LoadOrStore` to guarantee that only one limiter survives per tenant even under concurrent access).

**The business principle — two major conceptual rate-limiting models**, as presented in the introductory documentation:

| Model | Principle | Use case |
|---|---|---|
| **Token Bucket** | A bucket fills with tokens at a constant rate; each request consumes a token; empty bucket → blocked | Ideal for moderate bursts |
| **Leaky Bucket** | A bucket that leaks at a constant rate; requests arrive in bursts but leave at a steady pace | Ideal for smoothing traffic spikes |

### 7.7 Time window / TTL

The `RateLimiter` must also know when a limit resets. Depending on the chosen algorithm, this can be implemented with a fixed window, a sliding window, a token bucket, or a leaky bucket. For a first implementation, a simple, deterministic strategy remains preferable.

### 7.8 Why RateLimiter is not immediately integrated into Manager

`Manager` remains primarily responsible for `Request → Resolver → TenantID → Store → Tenant`. Rate limiting is an **additional** responsibility, which could be integrated into the pipeline (before or after full tenant resolution), but `Manager` must not be turned into an object aware of every concern in the toolkit — each calling middleware or component remains free to invoke it explicitly wherever relevant.

### 7.9 CacheKeyer — responsibility

Turn a logical application key into a key that is truly isolated per tenant:

```text
logical key: "user:123"
        ↓
tenant-A:user:123
```

### 7.10 CacheKeyer contract

```go
type CacheKeyer interface {
    Key(id TenantID, key string) string
}
```

Receives a `TenantID` and an application key, returns an isolated key:

```text
keyer.Key("tenant-A", "users:123")
        ↓
"tenant-A:users:123"
```

### 7.11 CacheKeyer stores nothing

It does neither `Get`, nor `Set`, nor `Delete` — only key construction:

```text
CacheKeyer
    │
    └── key construction

Cache
    │
    └── data storage
```

These two responsibilities remain strictly separate:

```text
                    Application
                         │
                         ▼
                 ┌──────────────┐
                 │ CacheKeyer   │
                 └──────┬───────┘
                        │
                        ▼
               tenant-A:user:123
                        │
                        ▼
                  ┌──────────┐
                  │  Cache   │
                  └──────────┘
```

The cache can be an in-memory, Redis, Memcached, or other implementation — `CacheKeyer` stays the same.

### 7.12 Isolation: the fundamental principle of this step

```text
                    TenantID
                       │
        ┌──────────────┼──────────────┐
        │              │              │
        ▼              ▼              ▼
      Store        CacheKeyer     RateLimiter
        │              │              │
        ▼              ▼              ▼
     Tenant       Isolated cache  Isolated limit
```

> **Every shared resource must be explicitly scoped by TenantID.**

This is what prevents one tenant from accidentally consuming another's quota, reading data cached for another tenant, causing key collisions, or bypassing the system's logical isolation.

### 7.13 Overall architecture after this step

```text
                         HTTP Request
                              │
                              ▼
                     ┌─────────────────┐
                     │    Resolver     │
                     └────────┬────────┘
                              │
                         TenantID
                              │
                              ▼
                     ┌─────────────────┐
                     │  RateLimiter    │
                     └────────┬────────┘
                              │
                    ┌─────────┴─────────┐
                    │                   │
                  DENY                ALLOW
                    │                   │
                    ▼                   ▼
                 HTTP 429           Store
                                        │
                                        ▼
                                     Tenant
                                        │
                         ┌──────────────┴──────────────┐
                         │                             │
                         ▼                             ▼
                    BanChecker                    tenantctx
                         │                             │
                         ▼                             ▼
                  tenant state                Application
                         │
                         ▼
                    Banned?
                         │
                  ┌──────┴──────┐
                  │             │
                 YES            NO
                  │             │
                  ▼             ▼
               Reject       Continue


       ┌─────────────────────────────────────┐
       │             Cache layer              │
       │                                     │
       │ TenantID + logical key              │
       │             │                       │
       │             ▼                       │
       │        CacheKeyer                   │
       │             │                       │
       │             ▼                       │
       │     tenant-A:users:123              │
       │             │                       │
       │             ▼                       │
       │           Cache                     │
       └─────────────────────────────────────┘
```

### 7.14 Agnosticism principle (reminder)

```text
             tenant
               │
       ┌───────┴────────┐
       │                │
   Interface       Interface
       │                │
       ▼                ▼
MemoryRateLimiter   CacheKeyer
       │
       ▼
Implementation

Then later:

RateLimiter
     │
     ├── MemoryRateLimiter
     │
     └── RedisRateLimiter (future evolution)
```

### 7.15 Tests to plan

**RateLimiter** — verify at minimum: the first request is allowed; requests up to the limit are allowed; a request exceeding the limit is rejected; a new window becomes allowed again; one tenant never blocks another; concurrent access produces no race condition (`go test -race`).

**CacheKeyer** — verify that `tenant-A + users:123` and `tenant-B + users:123` produce different keys (`tenant-A:users:123 != tenant-B:users:123`) — a fundamental isolation test.

### 7.16 Step 4 summary

| Component | Responsibility | Must not know about |
|---|---|---|
| `RateLimiter` | Limit requests per tenant | HTTP, Redis |
| `MemoryRateLimiter` | Local rate limiting implementation | HTTP logic |
| `CacheKeyer` | Build isolated keys | Cache storage |
| `Cache` | Store the data | Key-building logic |
| `TenantID` | Identify the tenant | Infrastructure |
| `Manager` | Resolve/retrieve the tenant | Cache details |
| `BanChecker` | Check for a ban | HTTP transport |

> **The fundamental rule of this step: every shared resource must be explicitly scoped by TenantID.**

---

## 8. Step 5 — RBAC and Metrics

### 8.1 Goal

After Step 4, the toolkit knows how to identify the tenant, retrieve its state, manage the cache, detect a ban, limit requests, and build isolated cache keys. Two questions remained open:

**Question 1 — Authorization.** *"Is this tenant allowed to perform this operation?"* — that's the role of **RBAC**.

**Question 2 — Observability.** *"How many requests are being processed? How many are rejected? How many tenants are active? How many bans occur?"* — that's the role of **Metrics**.

### 8.2 General architecture

```text
                         HTTP Request
                              │
                              ▼
                         Resolver
                              │
                              ▼
                           Tenant
                              │
                    ┌─────────┴─────────┐
                    │                   │
                    ▼                   ▼
              RateLimiter             RBAC
                    │                   │
                    │             Authorization
                    │                   │
                    └─────────┬─────────┘
                              │
                              ▼
                         BanChecker
                              │
                              ▼
                       Application
                              │
                              │
                    ┌─────────┴─────────┐
                    │                   │
                    ▼                   ▼
                 Metrics           Prometheus
```

RBAC (security/authorization) and Metrics (observability) are independent and must never be mixed.

### 8.3 Part 1 — RBAC

**Principle.** *Role-Based Access Control.* Instead of hardcoding *"Sylvinhio can do X"*, we define `Role → Permissions`, and a tenant then has one or more roles — via the `Roles []string` field already present on `Tenant` since Step 1.

**Concrete example**

```text
Tenant A          Tenant B
Roles: admin      Roles: viewer

admin
 ├── tenant.read
 ├── tenant.write
 ├── user.read
 └── user.write

viewer
 └── tenant.read

Tenant A + tenant.write → ALLOW
Tenant B + tenant.write → DENY
```

### 8.4 Separating role from permission

Above all, one should never hardcode `if tenant.Roles[0] == "admin" { ... }` throughout the application. Instead:

```text
Tenant
  │
  └── Roles
       │
       ▼
      RBAC
       │
       ▼
Permission
       │
       ├── ALLOW
       └── DENY
```

The application simply asks: *"does this tenant have this permission?"*, and the RBAC component handles the rest.

### 8.5 Minimal contract — `Authorizer` / `Can`

```go
type Authorizer interface {
    Can(t *Tenant, permission string) bool
}
```

The contract expresses only: *"can this tenant perform this action?"* — with no knowledge of HTTP, Gin, Echo, Redis, PostgreSQL, or Prometheus.

**Implementation adopted** — role/permission definitions are organized **per tenant** (not a single global role table shared by everyone), with a role's permissions represented as a **set** (`map[string]struct{}`) rather than a plain list, for O(1) lookups instead of a linear search:

```text
Tenant A
   │
   ├── admin
   │     ├── users:read
   │     ├── users:write
   │     └── users:delete
   │
   └── viewer
         └── users:read
```

This per-tenant organization is fundamental: two tenants can have a role with the **same name** but **completely different** permissions — tenant A's `admin` role implies nothing about what `admin` means for tenant B.

### 8.6 Why Tenant is provided to RBAC

RBAC should not make a second request to the Store to learn the roles — `Manager` has already retrieved the full `*Tenant`, including `Roles`. This avoids an unnecessary second fetch.

```text
Manager
   │
   ▼
Tenant
   │
   ├── ID
   ├── State
   └── Roles
          │
          ▼
        RBAC
```

### 8.7 Usage example

```go
allowed := rbac.Can(tenant, "users.create")
```

The toolkit remains agnostic about how the application translates a denial:

```text
RBAC
 │
 └── false
       │
       ├── HTTP → 403 Forbidden
       ├── gRPC → PermissionDenied
       └── CLI → error message
```

Same agnosticism principle as `AdminService` (see Step 7).

### 8.8 RBAC and multi-tenancy — two distinct questions

- **Tenant** answers: *"which isolated space does this request come from?"*
- **RBAC** answers: *"what can this actor do within that space?"*

```text
Resolver → Tenant A → RBAC → Permission
```

RBAC never replaces the tenant mechanism; it adds to it.

### 8.9 Evolvability — Role → Permissions → Action

A bad design freezes capabilities into an `if role == "admin"`. An evolvable architecture links a role to a list of permissions, which can be extended without touching application logic:

```text
admin
    users.read
    users.create
    users.update
    users.delete

teacher
    users.read

viewer
    users.read
```

### 8.10 Part 2 — Metrics

A production multi-tenant toolkit must be able to answer questions like: how many requests are received? how many are rejected? how many are blocked by the rate limiter? how many tenants are banned? how long does tenant resolution take? how many errors does the Store produce? That's the role of Metrics, with Prometheus envisioned as the exposition backend.

### 8.11 Why a Metrics abstraction

It would be harmful for `Manager` to directly contain `prometheus.CounterVec`/`prometheus.HistogramVec` types, making the core depend on `github.com/prometheus/client_golang` — losing agnosticism.

```text
tenant
 │
 └── Metrics contract
        │
        ├── NoopMetrics / MemoryMetrics (dev)
        │
        └── PrometheusMetrics (production)
```

### 8.12 Metrics contract (conceptual) and implementation adopted

The minimal conceptual contract initially envisioned:

```go
type Metrics interface {
    IncRequest()
    IncRBACDenied()
}
```

**The interface actually adopted and implemented**, closer to the real needs stated in the spec (functional requirement #5 — latency, RPS, error rate), exposes three operations parameterized by tenant:

```go
type MetricsCollector interface {
    IncRequests(ctx context.Context, tenantID tenant.TenantID)
    ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration)
    IncErrors(ctx context.Context, tenantID tenant.TenantID)
}
```

A `MemoryMetrics` implementation maintains, **per tenant**, `requests`, `errors`, `latencySum`, and `latencyCount` counters (allowing average latency to be computed), combining two levels of concurrency (see [section 14](#14-concurrency-and-thread-safety)): `sync.Map` for the dynamic collection of tenants, and `atomic.Int64` for each individual counter.

### 8.13 Types of metrics (Prometheus model)

**Counter** — a value that only increases (`tenant_requests_total`). Used to count requests, errors, RBAC denials, RateLimiter denials, bans.

**Histogram** — measures a distribution (`tenant_resolution_duration_seconds`), enabling detection of a performance degradation.

**Gauge** — a value that can go up and down (`tenants_active`).

### 8.14 Example of useful metrics

```text
tenant_requests_total
tenant_requests_rejected_total
tenant_ratelimit_rejected_total
tenant_rbac_denied_total
tenant_resolution_errors_total
tenant_resolution_duration_seconds
tenant_banned_total
```

The goal is not to create hundreds of metrics, but to favor a small number of genuinely useful ones.

### 8.15 Caution: Prometheus label cardinality

A particularly important point in a multi-tenant system: each label combination creates a distinct Prometheus time series. Using `tenant_id` directly as a label for a platform with tens of thousands of tenants can create a cardinality explosion.

```text
Bad idea
tenant_requests_total{tenant_id="..."} for every tenant without thought

Preferable
tenant_requests_total{status="success", source="api"}
tenant_rbac_denied_total{permission="users.read"}
```

> **Rule adopted: never use a high-cardinality piece of user data as a Prometheus label without justification — particularly true for `TenantID`.**

### 8.16 RBAC / Metrics separation

`RBAC → Prometheus` directly should never happen. RBAC does its job (`Can(...)`), then an upper layer records the result in the metrics:

```text
             ┌──────────────┐
             │     RBAC     │
             └──────┬───────┘
                    │
                 result
                    │
                    ▼
             ┌──────────────┐
             │    Metrics   │
             └──────┬───────┘
                    │
                    ▼
                Prometheus
```

Otherwise RBAC would become dependent on Prometheus, breaking agnosticism.

### 8.17 Package architecture

```text
tenant-core/
│
├── tenant.go
│
├── tenantctx/
├── resolver/
├── store/
├── eventbus/
│
├── middleware/
│   ├── nethttp/
│   ├── gin/
│   ├── echo/
│   └── chi/
│
├── admin/
├── tenanttest/
├── rbac/
│
└── metrics/
    └── prometheus/ (envisioned adapter)
```

### 8.18 Relation to Go interfaces (structural typing, reminder)

```go
type Metrics interface {
    IncRequest()
    IncRBACDenied()
}
```

A `PrometheusMetrics` implementation having the right methods automatically satisfies `tenant.Metrics` with no explicit declaration — same mechanism as `tenant.Store`, `tenant.Resolver`, `tenant.AdminStore`, `eventbus.EventBus`.

### 8.19 Tests

**RBAC** — test `admin + users.read → ALLOW`, `admin + users.delete → ALLOW`, `viewer + users.read → ALLOW`, `viewer + users.delete → DENY`, a tenant with no role → `DENY`, several roles → correct behavior, and especially verify that a tenant never gets another's permissions (unknown tenant, unknown role).

**Metrics** — verify that a request increments the right counter, that an RBAC denial increments its dedicated counter, that a RateLimiter denial increments its own. For Prometheus, also verify that the metrics produced are correctly exposed in the expected format.

### 8.20 What this step adds to the toolkit

```text
Before                          After Step 5
Toolkit                        Toolkit
   │                              │
   ├── Identification             ├── Identification (Resolver)
   ├── Storage                    ├── Isolation (Store, CacheKeyer, tenantctx)
   ├── Cache                      ├── Security (BanChecker, RateLimiter, RBAC)
   ├── Ban                        └── Observability (Metrics → Prometheus)
   └── Rate limiting
```

### 8.21 Architectural principles adopted

1. **RBAC doesn't know HTTP** — RBAC produces an authorization decision; HTTP translates it into a 403.
2. **Metrics doesn't know the business logic** — Metrics measures; Prometheus collects.
3. **The core doesn't depend on Prometheus** — `tenant → Metrics contract → Prometheus adapter`.
4. **The tenant remains the isolation boundary** — `TenantID → Store / RateLimiter / Cache / RBAC`.
5. **Interfaces stay minimal** — each component exposes only what its consumer needs.

### 8.22 Step 5 summary

| Component | Responsibility |
|---|---|
| `RBAC` | Check a tenant's permissions |
| `Role` | Group permissions together |
| `Permission` | Represent a business capability |
| `Authorizer` / `Can` | Authorization contract |
| `Metrics` / `MetricsCollector` | Observability contract |
| `MemoryMetrics` / `PrometheusMetrics` | Contract implementations |
| `Counter` | Count events |
| `Histogram` | Measure durations/distributions |
| `Gauge` | Measure a variable value |

> **RBAC decides "who can do what", while Metrics lets you know "what's actually happening in the system".**

Coherent progression across the first five steps: identification → data → security → resource protection → authorization + observability.

---

## 9. Step 6 — Framework adapters

### 9.1 The problem to solve

The `tenant-core` core contains framework-independent logic:

```text
HTTP Request
     │
     ▼
Identify the tenant
     │
     ▼
Retrieve the tenant
     │
     ▼
Put the tenant in the context
     │
     ▼
Application handler
```

But each Go framework builds its middlewares differently:

```text
net/http    func(next http.Handler) http.Handler
Gin         func(c *gin.Context)
Echo        func(next echo.HandlerFunc) echo.HandlerFunc
Chi         func(next http.Handler) http.Handler
```

The goal: above all, never rewrite the multi-tenant logic four times. Hence the **Framework Adapters**.

### 9.2 General architecture

```text
                       ┌──────────────────────┐
                       │     Application      │
                       │   Go + HTTP Framework │
                       └──────────┬───────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │                           │
                 Middleware                 Middleware
                    │                           │
          ┌─────────┼─────────┬─────────┬──────┘
          │         │         │         │
          ▼         ▼         ▼         ▼
       net/http    Gin       Echo      Chi
          │         │         │         │
          └─────────┴─────────┼─────────┘
                              │
                              ▼
                       tenant.Manager
                              │
                              ▼
                    ┌──────────────────┐
                    │    Resolver      │
                    │ "Which tenant?"  │
                    └────────┬─────────┘
                             │
                             ▼
                    ┌──────────────────┐
                    │      Store       │
                    │ "What is its     │
                    │      state?"     │
                    └────────┬─────────┘
                             │
                             ▼
                       *tenant.Tenant
                             │
                             ▼
                    tenantctx.WithTenant()
                             │
                             ▼
                       HTTP Request
                             │
                             ▼
                     Business handler
```

> **Frameworks live outside the toolkit's core. The core knows neither Gin, Echo, nor Chi.**

### 9.3 The core: `tenant.Manager`

```go
type Manager struct {
    resolver Resolver
    store    Store
}

func (m *Manager) Resolve(r *http.Request) (*Tenant, error)
```

```text
HTTP Request
     │
     ▼
Resolver.Resolve()
     │
     ▼
TenantID
     │
     ▼
Store.Get()
     │
     ▼
*Tenant
```

`Manager` has absolutely no idea whether the request comes from Gin, Echo, Chi, or `net/http` — and this is **deliberate**.

**Important design note**: `Manager.Resolve()` does **not** build a `context.Context` itself. Making `tenant.go` (root package) depend on `tenantctx` would create a circular dependency (`tenant → tenantctx → tenant`, since `tenantctx` already depends on `tenant` for the `*Tenant` type). It is therefore each framework adapter's responsibility to combine `Manager.Resolve()` and `tenantctx.WithTenant()`.

**Fail-fast at construction**: `tenant.New(options...)` panics if `Resolver` or `Store` are not provided after applying the options — a missing required dependency is a program configuration error, caught immediately, not a request-processing error handled via `error`.

### 9.4 The role of tenantctx

Once `Manager` provides `*tenant.Tenant`, this information needs to be passed to handlers via the standard `context.Context`:

```go
ctx := tenantctx.WithTenant(r.Context(), t)
```

Then replace the request with this new context. The handler can then do:

```go
t := tenantctx.FromContext(r.Context())
```

```go
func GetUsers(w http.ResponseWriter, r *http.Request) {
    t := tenantctx.FromContext(r.Context())
    // use t...
}
```

This business logic works identically behind all four adapters.

### 9.5 net/http adapter

**File**: `middleware/nethttp.go`

**Signature**: `func Wrap(m *tenant.Manager, next http.Handler) http.Handler`

```text
Request
   │
   ▼
m.Resolve(r)
   │
   ├── error → HTTP 404
   │
   ▼
Tenant
   │
   ▼
tenantctx.WithTenant(...)
   │
   ▼
r.WithContext(...)
   │
   ▼
next.ServeHTTP(...)
```

**Essential code**

```go
ctx := tenantctx.WithTenant(r.Context(), t)
next.ServeHTTP(w, r.WithContext(ctx))
```

This is the reference adapter, directly using Go's standard HTTP primitives. If `Manager.Resolve()` fails, the request is rejected with a `404` status **before** reaching `next` — `next.ServeHTTP` is **never** called in that case (behavior explicitly verified by a test).

**Important detail: why `r.WithContext(ctx)`, not `r` directly?** `context.Context` is immutable in Go — `WithTenant()` creates a new context, it never modifies the old one. Likewise, `r.WithContext()` does not modify `r` in place: it returns a **copy** of the request carrying the new context. Without this call, the next handler would always receive the old context (without the tenant), and `tenantctx.FromContext` would never find anything.

### 9.6 Gin adapter

**File**: `middleware/gin/gin.go` — separate Go sub-module (its own `go.mod`, dependency on `github.com/gin-gonic/gin`).

Gin has its own `*gin.Context`, but it always contains a standard HTTP request accessible via `c.Request`.

```go
t, err := m.Resolve(c.Request)
ctx := tenantctx.WithTenant(c.Request.Context(), t)
c.Request = c.Request.WithContext(ctx)
c.Next()
```

```text
gin.Context
     │
     ▼
c.Request
     │
     ▼
Manager.Resolve()
     │
     ▼
Tenant
     │
     ▼
tenantctx
     │
     ▼
c.Request = new Request
     │
     ▼
c.Next()
```

On a resolution failure, `c.AbortWithStatus(http.StatusNotFound)` is used — Gin's equivalent of "never call the next handler".

**Why not use `c.Set("tenant", t)`?** That would create a propagation mechanism specific to Gin. The choice adopted (`tenantctx.WithTenant`) guarantees that the tenant stays accessible with the **same API everywhere**, regardless of the framework — essential cross-cutting consistency for a toolkit meant for thousands of developers across different stacks.

**Setup note**: the `middleware/gin` sub-module uses a `replace github.com/sylvinhio676-ux/tenant-core => ../..` directive in its `go.mod`, to point at the local code during development (before the root module has a published tagged version). This directive will need to be removed once a stable version is published, so users pull the real dependency from the public repository.

### 9.7 Echo adapter

**File**: `middleware/echo/echo.go` — separate Go sub-module (dependency on `github.com/labstack/echo/v4`).

Echo has `echo.Context`, but the HTTP request is obtained via a **method**, `c.Request()`, not a direct field.

**Signature**: `func Middleware(m *tenant.Manager) echo.MiddlewareFunc`

```text
echo.Context
     │
     ▼
c.Request()
     │
     ▼
Manager.Resolve()
     │
     ▼
Tenant
     │
     ▼
tenantctx.WithTenant()
     │
     ▼
c.Request().WithContext()
     │
     ▼
c.SetRequest(...)
     │
     ▼
next(c)
```

**Important detail**: `c.SetRequest(c.Request().WithContext(ctx))`. Unlike Gin, you can't simply do `c.Request = ...` — Echo exposes the request via an accessor method, not a public field.

**Error handling**: Echo propagates errors through each handler's `error` return value, not by writing directly to the `ResponseWriter`:

```go
return echo.NewHTTPError(http.StatusNotFound, "tenant not found")
```

To stop the middleware chain on rejection, `c.Next()` (well, `next(c)`) is simply never reached — the function returns the error before that.

### 9.8 Chi adapter

**File**: `middleware/chi/chi.go` — separate Go sub-module (dependency on `github.com/go-chi/chi/v5`).

Chi is the closest to `net/http`: it consumes `http.Handler` directly, with no context type of its own.

**Signature**: `func Middleware(m *tenant.Manager) func(http.Handler) http.Handler`

```text
http.Request
     │
     ▼
Manager.Resolve()
     │
     ▼
Tenant
     │
     ▼
tenantctx.WithTenant()
     │
     ▼
r.WithContext()
     │
     ▼
next.ServeHTTP()
```

**Essential code**

```go
ctx := tenantctx.WithTenant(r.Context(), t)
r = r.WithContext(ctx)
next.ServeHTTP(w, r)
```

Because Chi relies directly on `http.Handler`, it needs no extra context system — the code is nearly identical to the `net/http` adapter.

### 9.9 Comparing the four adapters

| Framework | Request access | Injection | Continue |
|---|---|---|---|
| `net/http` | `r` | `r.WithContext()` | `next.ServeHTTP()` |
| Gin | `c.Request` | `c.Request.WithContext()` | `c.Next()` |
| Echo | `c.Request()` | `c.SetRequest()` | `next(c)` |
| Chi | `r` | `r.WithContext()` | `next.ServeHTTP()` |

Despite these syntactic differences, the architectural outcome is identical:

```text
                 ┌───────────────────────┐
                 │    Manager.Resolve()  │
                 └───────────┬───────────┘
                             │
                             ▼
                         *Tenant
                             │
                             ▼
                  tenantctx.WithTenant()
                             │
                             ▼
                     new Context
                             │
                             ▼
                      Next handler
```

### 9.10 Why four adapters

```go
// Gin
router.Use(gin.Middleware(manager))

// Echo
e.Use(echo.Middleware(manager))

// Chi
r.Use(chi.Middleware(manager))

// net/http alone
handler := nethttp.Wrap(manager, myHandler)
```

The toolkit's business logic never changes. This is exactly the role of an *adapter*: translate a framework's specific interface into the core's generic interface.

### 9.11 What adapters do NOT do

This is a deliberately strict boundary, documented to prevent future drift:

- ❌ decide how a tenant works
- ❌ query the database directly
- ❌ check RBAC roles
- ❌ apply the RateLimiter
- ❌ handle metrics
- ❌ handle bans directly
- ❌ know the business logic

An adapter exclusively does:

```text
Framework
    ↓
extract *http.Request
    ↓
Manager.Resolve()
    ↓
inject Tenant into Context
    ↓
Framework
```

Nothing more.

### 9.12 Complete toolkit architecture after Step 6

```text
                         ┌─────────────────────┐
                         │    HTTP Request     │
                         └──────────┬──────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
                 ▼                  ▼                  ▼
              Resolver          Middleware          ...
                 │                  │
                 │                  ▼
                 │             Manager
                 │                  │
                 │          ┌───────┴───────┐
                 │          │               │
                 ▼          ▼               ▼
             TenantID     Resolver        Store
                            │               │
                            └───────┬───────┘
                                    │
                                    ▼
                              *Tenant
                                    │
                                    ▼
                              tenantctx
                                    │
                                    ▼
                            Business handler
                                    │
               ┌────────────────────┼──────────────────┐
               │                    │                  │
               ▼                    ▼                  ▼
          BanChecker            RateLimiter          RBAC
               │                    │                  │
               └────────────────────┼──────────────────┘
                                    │
                                    ▼
                                Metrics
```

```text
                 APPLICATION
                      │
        ┌─────────────┼─────────────┐
        │             │             │
      Gin           Echo          Chi
        │             │             │
        └─────────────┼─────────────┘
                      │
                      ▼
                tenant.Manager
                      │
                      ▼
                Tenant Context
```

> **The core of our toolkit speaks in Go abstractions (Manager, Resolver, Store, context.Context). The framework adapters simply translate each framework's conventions into these abstractions. This is what lets our multi-tenant code stay framework-independent while remaining very easy to integrate.**

---

## 10. Step 7 — Admin API and Redis EventBus

This step had a dual goal: **tenant administration** via an HTTP API, and **cross-instance propagation**, so that a tenant change (notably a ban) is immediately known by every server instance thanks to Redis Pub/Sub.

> **The business core knows neither HTTP, nor Redis, nor any particular framework. Adapters depend on the core, never the other way around.**

### 10.1 Overall architecture of this step

```text
                         APPLICATION
                              │
                    ┌─────────┴─────────┐
                    │                   │
              Admin API             Normal request
                    │                   │
                    ▼                   ▼
             admin.HTTPHandler      Manager.Resolve()
                    │                   │
                    ▼                   ▼
              admin.Service       Resolver + Store
                    │
             ┌──────┴──────┐
             │             │
             ▼             ▼
        AdminStore      EventBus
             │             │
             ▼             ▼
        MemoryStore    RedisEventBus
                           │
                           ▼
                      Redis Pub/Sub
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
           Instance A   Instance B   Instance C
              │            │            │
              ▼            ▼            ▼
         BanChecker    BanChecker    BanChecker
```

`admin.Service` doesn't know it uses Redis — it only knows `eventbus.EventBus`. Similarly, it doesn't know about `MemoryStore` or any particular SQL database — it knows `tenant.AdminStore`.

### 10.2 Extending tenant.Store — why a separate interface

The existing `Store` interface, dedicated to the normal read path:

```go
type Store interface {
    Get(ctx context.Context, id TenantID) (*Tenant, error)
    IsBanned(ctx context.Context, id TenantID) (bool, error)
}
```

... was **not** enriched with administration operations. Instead:

```go
type AdminStore interface {
    Create(ctx context.Context, t *Tenant) error
    Update(ctx context.Context, t *Tenant) error
    SetState(ctx context.Context, id TenantID, state State) error
}
```

**Why?** Because the minimal-interfaces principle had to be respected: `Manager` has absolutely no need to be able to ban a tenant, so it must not depend on an interface containing `Ban()`/`Disable()`/`Activate()`. This avoids progressively turning `Store` into one giant CRUD interface.

### 10.3 AdminStore: why not `Ban()` / `Disable()` / `Activate()` directly

`AdminStore` deliberately only exposes `Create`, `Update`, `SetState` — never `Ban()`, `Disable()`, `Activate()` directly, because these operations are not simple local modifications: a ban must **also** produce an event.

```text
Tenant A
   │
   ├── local state → Banned
   │
   └── event → TenantEvent
```

If `AdminStore` had `Ban()`, a developer could call `store.Ban(ctx, id)` and **forget** to publish the event, creating an inconsistency:

```text
Instance A: Tenant = BANNED     ❌ event not published
Instance B: Tenant = ACTIVE
```

That is precisely the consistency problem the architecture wanted to avoid — publishing the event must never be an optional step left to the caller's discretion.

### 10.4 MemoryStore and the pointer problem (detailed reminder)

This problem was identified precisely while adding `SetState`, and was already documented in Step 2 (section 5.6) — restated here with the specific use case of `SetState`:

```text
map[TenantID]*Tenant
```

When `t, _ := store.Get(...)` returns `*Tenant`, that pointer refers to the **same object** present in the map — it's not a copy. Doing `t.State = tenant.Banned` outside the lock can trigger:

```text
Goroutine A                 Goroutine B

t.State = Banned
       │
       │                 Get()
       │                   │
       ▼                   ▼
   write                 read
```

... a genuine data race.

> **Protecting only the map is not enough when the map's values are mutable pointers.**

Write operations therefore remain properly protected by the store's synchronization mechanism: `Get()` returns a copy, `SetState`/`Create`/`Update` operate directly on the internal object under an exclusive `Lock()`.

### 10.5 admin.Service: the business core of administration

```text
admin/
├── admin.go
├── admin_test.go
└── http.go
```

```go
type Service struct {
    store tenant.AdminStore
    bus   eventbus.EventBus
}
```

Only two required dependencies. Simple constructor, no functional options:

```go
func NewAdminService(store tenant.AdminStore, bus eventbus.EventBus) *Service
```

**Why no functional options here, unlike `Manager`?** `Service` only has two required dependencies and no planned optional configuration. `NewAdminService(store, bus)` is simpler and more readable than `NewAdminService(WithStore(...), WithEventBus(...))` for such a small, fixed number of parameters — the functional-options pattern is only useful when it brings genuine extensibility value, not as a systematic reflex.

### 10.6 The `transition()` method

`Ban()`, `Disable()`, `Activate()` share exactly the same mechanism:

1. change the state;
2. build the `TenantEvent`;
3. publish the event;
4. log if publication fails.

Rather than duplicating this logic three times, a common private method factors it out:

```go
func (s *Service) transition(ctx context.Context, id tenant.TenantID, state tenant.State) error
```

```go
func (s *Service) Ban(...) error {
    return s.transition(ctx, id, tenant.Banned)
}

func (s *Service) Disable(...) error {
    return s.transition(ctx, id, tenant.Disabled)
}

func (s *Service) Activate(...) error {
    return s.transition(ctx, id, tenant.Active)
}
```

> **A single implementation of the shared logic, several explicit business operations.** This also guarantees that behavior (including logging on failure) stays identical across the three transitions, with no risk of accidental divergence.

### 10.7 The flow of a ban

```text
service.Ban(ctx, "tenant-A")

                 AdminService.Ban()
                         │
                         ▼
                  transition()
                         │
                         ▼
             AdminStore.SetState()
                         │
                         ▼
                 State = Banned
                         │
                         ▼
              create TenantEvent
                         │
                         ▼
                EventBus.Publish()
                         │
                         ▼
                event propagation
```

```go
eventbus.TenantEvent{
    TenantID:  id,
    State:     tenant.Banned,
    Timestamp: time.Now(),
}
```

### 10.8 Why SetState → Publish (and not the reverse)

**Architectural decision, non-atomicity accepted.**

```text
SetState() → success
Publish()  → failure
```

The local state becomes `BANNED`, but other instances don't receive the event — an inconsistency, but **accepted** for this version.

**Why not `Publish → SetState`?** Because then `Tenant A → BANNED` could be published while `SetState()` subsequently fails — the event would announce a state that ultimately never exists in the Store. That is strictly worse: a **lying event**.

**Decision adopted**: `SetState → Publish`, with a limitation explicitly documented right in the code itself:

```text
// Known limitation: SetState and Publish are not atomic with each other (they
// are two distinct systems). The order SetState → Publish guarantees that we
// never publish an event for a state that was not actually
// applied to the Store — but if Publish fails after a successful SetState,
// the event may be lost until manual resynchronization or a
// future durable-delivery mechanism (Outbox pattern).
```

### 10.9 Logging the inconsistency

When `SetState()` succeeds but `Publish()` fails, the service **explicitly logs** the anomaly, with full context (the tenant involved, the target state, the error encountered):

```text
ERROR
tenant state changed but event publication failed
tenant_id=tenant-A state=banned error=redis connection refused
```

This lets an operator know: ⚠ local state changed, ⚠ event not propagated, ⚠ resynchronization potentially needed.

**Important nuance adopted**: the log never replaces the error returned to the caller — both are done, because the caller alone (receiving just a generic Redis error) wouldn't necessarily know that a business operation *partially* succeeded (the Store was indeed modified) — information only the `Service` has.

**Future evolution identified**: an **Outbox** pattern (state change and event-to-publish written in the same storage transaction, with an asynchronous *worker* responsible for actual publication and retrying on failure) would make publication durable. This mechanism was deliberately **not** built at this step.

### 10.10 Admin API — HTTP layer

```go
type HTTPHandler struct {
    mux     *http.ServeMux
    service *Service
}
```

**Important architectural choice: pure `net/http`, neither Gin, Echo, nor Chi.**

**Why?** Because the Admin API is a **command API** for the toolkit, not a middleware meant to be plugged into different application frameworks. It therefore stays independent from whatever framework is used by the application consuming the toolkit — any Go server able to mount an `http.Handler` can integrate it, regardless of its own framework choice for the rest of the application.

### 10.11 Modern routing with `http.ServeMux` (Go 1.22+)

```go
h.mux.HandleFunc("PATCH /tenants/{id}/ban", h.handleBan)
h.mux.HandleFunc("PATCH /tenants/{id}/disable", h.handleDisable)
h.mux.HandleFunc("PATCH /tenants/{id}/activate", h.handleActivate)
```

Thanks to `ServeMux`'s modern pattern support (HTTP methods + wildcards), the handler retrieves directly:

```go
id := r.PathValue("id")
```

```text
PATCH /tenants/tenant-A/ban
                 │
                 ▼
             PathValue
                 │
                 ▼
             tenant-A
```

This avoids manual parsing (`strings.Split`, `strings.TrimPrefix`, `switch`), reducing the risk of progressively rebuilding a homemade mini-router.

### 10.12 Why only three endpoints

Deliberately, **no** `POST /tenants` nor `GET /tenants/{id}`, even though `AdminStore` has `Create()` and `Store` has `Get()`.

> **The HTTP API must follow the Service's business contract, not automatically expose every Store method.**

Currently, `Service` only exposes `Ban()`, `Disable()`, `Activate()` — so the API exposes exactly `PATCH /tenants/{id}/ban`, `/disable`, `/activate`, not a generic CRUD. This protects the architecture against a *"Store → all methods → HTTP endpoints"* kind of drift.

If creation or reading should one day be part of the Admin API, the approach is to first enrich the business contract (`Service.Create(...)`, `Service.Get(...)`), **and only then** expose the corresponding endpoints — never the other way around.

### 10.13 Admin API architecture — full flow

```text
HTTP
 │
 │ PATCH /tenants/tenant-A/ban
 ▼
HTTPHandler
 │
 │ service.Ban(...)
 ▼
AdminService
 │
 ├───────────────┐
 ▼               ▼
AdminStore     EventBus
 │               │
 ▼               ▼
State=Banned   TenantEvent
                 │
                 ▼
             propagation
```

The `HTTPHandler` knows nothing about Redis, `MemoryStore`, how the state is stored, or how events are transported.

### 10.14 Current limitations of the Admin API (honestly documented)

**Authentication** — the API currently has **no** authentication or authorization at all. It must therefore not be exposed directly to the Internet in production. A critical point to address later.

**Error handling** — `writeError(...)` systematically returns `500 Internal Server Error`, regardless of the actual cause (tenant not found, store unavailable, etc.). A future evolution should distinguish `404` (tenant absent), `500` (internal error), `503` (dependency unavailable). This limitation notably stems from the fact that there is not yet an exported sentinel error for "tenant not found" at the level of the generic `AdminStore` interface — unlike `store.ErrTenantNotFound`, which is specific to `MemoryStore`.

### 10.15 Why Redis

So far, `MemoryEventBus` works very well in a single instance:

```text
Instance A
   │
MemoryEventBus
   │
local handlers
```

But with several instances, each has its own memory:

```text
Instance A
   │
Ban tenant-A
   │
MemoryEventBus
   │
   └── only A
```

B and C see nothing.

### 10.16 Redis Pub/Sub — RedisEventBus

```text
eventbus/redis/
└── redis.go
```

Uses `github.com/redis/go-redis/v9`. The `eventbus` package itself **never** knows about Redis — an essential architectural rule:

```text
eventbus
   │
   │ defines
   ▼
EventBus interface
   ▲
   │ implements
   │
eventbus/redis
```

Thanks to Go's structural typing, `RedisEventBus` automatically satisfies `eventbus.EventBus`.

```go
type RedisEventBus struct {
    client  *goredis.Client
    channel string

    mu      sync.Mutex
    pubsubs []*goredis.PubSub // tracked so Stop can close them
}
```

```text
RedisEventBus
├── Redis Client
├── Redis Channel
└── pubsubs (one *redis.PubSub per Subscribe call, for Stop)
```

**Setup note**: a separate Go sub-module (`eventbus/redis/go.mod`), with the same local `replace` directive as the framework adapters, for the same reasons.

**Network resilience is handled natively by go-redis — verified from source, not assumed.** `go-redis/v9`'s `*redis.PubSub` type documents itself as automatically reconnecting and resubscribing to its channels on network errors, and this was confirmed by reading `pubsub.go` (v9.22.0) directly: the goroutine behind `pubsub.Channel()` calls `Receive()` in a loop and only stops (closing the Go channel) when `Close()` has been called explicitly (returning `pool.ErrClosed`); any other error — including a lost connection — is retried transparently, with automatic resubscription to the same channels and a periodic health-check ping (every 3s by default) to catch silent disconnects. Concretely, this means:

- The `for msg := range pubsub.Channel()` loop inside `Subscribe()` never exits because of a network blip — messages simply resume flowing once go-redis has reconnected.
- Building a custom backoff/retry/resubscribe mechanism on top of this would duplicate logic go-redis already provides, for no benefit — and was considered and deliberately rejected for exactly that reason.
- `RedisEventBus.Stop()` exists for a different purpose: an intentional, clean shutdown. It closes every `*redis.PubSub` created by `Subscribe`, which ends their `Channel()` loop and the associated goroutine. It is unrelated to reconnection and safe to call multiple times, or even if `Subscribe` was never called.
- **Race between `Stop()` and an in-flight `Subscribe()`**: `Subscribe()`'s `pubsub.Receive(ctx)` can block for a while against a slow or degraded Redis, so `Stop()` could run — and close every subscription it currently knows about — before that `Subscribe()` call finishes. To avoid silently leaking that subscription (registered after `Stop()` already ran, and therefore never closed), `RedisEventBus` tracks a `stopped` flag: once `Receive()` succeeds, `Subscribe()` re-checks `stopped` under the same lock before registering the subscription; if `Stop()` already ran, it closes its own `pubsub` immediately instead and returns `ErrStopped`. This makes `Stop()` a true, final stop — a `RedisEventBus` cannot be revived by a `Subscribe()` call that was merely running late.

### 10.17 TenantEvent ↔ JSON transformation

Redis doesn't know `eventbus.TenantEvent` — it carries raw bytes/messages. JSON was chosen.

**Publishing**

```text
TenantEvent
     │
     ▼
json.Marshal()
     │
     ▼
[]byte
     │
     ▼
Redis PUBLISH
```

```json
{
  "TenantID": "tenant-A",
  "State": "banned",
  "Timestamp": "..."
}
```

**Receiving**

```text
Redis
 │
 │ JSON message
 ▼
RedisEventBus
 │
 ▼
json.Unmarshal()
 │
 ▼
TenantEvent
 │
 ▼
handler(event)
```

Symmetric transformation:

```text
             Publish
TenantEvent ────────→ JSON ────────→ Redis
                                      │
                                      │
TenantEvent ←────── JSON ←───────────┘
             Subscribe
```

**Why JSON**: standard, readable, simple, language-independent, directly supported by the Go standard library.

### 10.18 Subscribe() and the dedicated goroutine

Redis Pub/Sub works with a **continuous** subscription:

```go
for msg := range pubsub.Channel() {
    // ...
}
```

This loop can live for the entire lifetime of the server. If it ran directly inside `Subscribe()`, the function would never return, blocking all calling code:

```text
Subscribe()
   │
   ▼
infinite loop
   │
   ├── never returns
   │
   └── calling code blocked
```

**Solution adopted**:

```text
Subscribe()
   │
   ├── configuration
   │
   ├── confirmation
   │
   └── launch goroutine
             │
             ▼
       reading loop
```

The receiving loop lives in a single, dedicated, permanent goroutine — distinct from the goroutines then launched for each individual handler (see 10.19).

### 10.19 Synchronous confirmation with `pubsub.Receive()` — fail-fast

Simply doing `pubsub := client.Subscribe(...)` does **not** immediately guarantee that Redis has confirmed the subscription (an asynchronous operation on the connection side). `pubsub.Receive(ctx)` is used **before** launching the processing goroutine, to block until confirmation, or surface a concrete error if Redis is unreachable:

```text
Subscribe()
    │
    ▼
Redis SUBSCRIBE
    │
    ▼
Receive()
    │
    ├── error → Subscribe returns error
    │
    └── confirmation
           │
           ▼
       goroutine
           │
           ▼
       messages
```

This follows the **fail-fast** principle for configuration errors: a developer who misconfigures Redis (wrong address, invalid credentials) discovers it immediately when their server starts, rather than silently in production, hours later.

### 10.20 Protection against panicking handlers (reminder + application to Redis)

Same behavior as `MemoryEventBus` (Step 3): each received event is handled in its own goroutine, protected by `recover()`:

```text
Redis message
      │
      ▼
JSON decode
      │
      ▼
go safeCall(handler, event)
```

```go
func safeCall(handler func(TenantEvent), event TenantEvent) {
    defer func() {
        if r := recover(); r != nil {
            // log
        }
    }()
    handler(event)
}
```

```text
Handler A → panic → recover()
Handler B → continues normally
Handler C → continues normally
```

A failing handler must never bring down the process.

### 10.21 Malformed Redis message

If `json.Unmarshal(...)` fails, the invalid message is logged then **ignored**, with no `panic(...)` or `return` that would stop all consumption:

```text
invalid message
      │
      ▼
log error
      │
      ▼
continue
```

### 10.22 Final multi-instance architecture

```text
                 Redis
              Pub/Sub Channel
                   │
       ┌───────────┼───────────┐
       │           │           │
       ▼           ▼           ▼
 Instance A   Instance B   Instance C
       │           │           │
 RedisEventBus RedisEventBus RedisEventBus
       │           │           │
 BanChecker   BanChecker   BanChecker
```

A ban performed on instance A propagates to all others:

```text
                    Instance A
                        │
                   Admin API
                        │
                        ▼
                  AdminService
                        │
                  SetState(Banned)
                        │
                        ▼
                  Publish(Event)
                        │
                        ▼
                      Redis
                        │
             ┌──────────┼──────────┐
             ▼          ▼          ▼
            A           B          C
             │          │          │
             ▼          ▼          ▼
         handlers    handlers   handlers
             │          │          │
             ▼          ▼          ▼
        BanChecker  BanChecker  BanChecker
```

### 10.23 Test strategy — why miniredis rather than a real Redis

To test `RedisEventBus` without requiring a real Redis server during `go test` (neither for the local developer nor in CI), the **`miniredis`** library (a pure in-memory Redis implementation, written in Go) was chosen over installing a real Redis in the CI workflow.

| Criterion | miniredis | Redis in CI |
|---|---|---|
| Redis installed locally | ❌ not required | ❌ not required |
| External process | ❌ no | ✅ yes |
| Speed | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |
| Immediate `go test` | ✅ | ❌ requires CI config |
| Reproducibility | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| Testing real Redis | ⚠️ simulation | ✅ yes |

**Decision**: `miniredis` for now, guaranteeing that `go test ./...` works everywhere with no external dependency — consistent with the testability principle applied from the start. An integration test with a real Redis remains a possible complementary evolution, not a replacement.

The tests cover: the happy path (publish an event, receive it, verify the JSON round-trip with a tolerance on the timestamp via `assert.WithinDuration`), and the fail-fast case (`Subscribe()` must fail immediately if Redis is unreachable, not silently).

### 10.24 Complete architecture of Step 7

```text
tenant-core/
│
├── tenant.go
│   ├── Tenant
│   ├── TenantID
│   ├── State
│   ├── Resolver
│   ├── Store
│   └── AdminStore
│
├── admin/
│   ├── admin.go
│   │   └── Service
│   │       ├── Ban
│   │       ├── Disable
│   │       ├── Activate
│   │       └── transition
│   │
│   ├── http.go
│   │   └── HTTPHandler
│   │
│   └── admin_test.go
│
├── eventbus/
│   ├── eventbus.go
│   │   └── EventBus
│   │
│   ├── memory.go
│   │   └── MemoryEventBus
│   │
│   └── redis/            (separate Go sub-module)
│       ├── go.mod
│       └── redis.go
│           └── RedisEventBus
│
└── store/
    └── memory.go
        └── MemoryStore
```

### 10.25 The architectural principle to remember

```text
                    BUSINESS
                      │
                      ▼
                AdminService
                      │
          ┌───────────┴───────────┐
          ▼                       ▼
      AdminStore              EventBus
          │                       │
          ▼                       ▼
    MemoryStore              MemoryEventBus
                                  │
                                  │
                             RedisEventBus
                                  │
                                  ▼
                                Redis
```

`AdminService` ❌ doesn't know HTTP, ❌ doesn't know Redis, ❌ doesn't know Gin, ❌ doesn't know Echo, ❌ doesn't know PostgreSQL.

`AdminService` ✅ knows `AdminStore`, ✅ knows `EventBus`.

> **The core defines the contracts. The adapters implement these contracts.** Exactly the same principle applied with the middleware adapters (Step 6).

### 10.26 What is deliberately left for later

| Topic | Current state | Evolution |
|---|---|---|
| Admin API | Functional for transitions | Authentication/RBAC |
| HTTP errors | Mostly `500` | Mapping `404`/`409`/`500`/`503` |
| SetState → Publish | Not atomic | Outbox pattern |
| EventBus | Redis Pub/Sub, with a `Stop()` for clean shutdown (reconnection/resubscription on network errors is handled natively by go-redis's `*redis.PubSub` — see §10.16) | — |
| Redis | Real-time propagation | Dedicated monitoring, propagation latency metrics |
| Tenant creation | `AdminStore.Create` exists | Add the `Service.Create` business capability if needed |
| Admin reading | `Store.Get` exists | Add `Service.Get` if the business need arises |
| Redis tests | Covered via `miniredis` | Integration tests with a real Redis server, as a complement |

**In one sentence**: Step 7 turns the toolkit from a system able to resolve a tenant into a system able to manage its lifecycle and propagate its state changes across multiple instances, while keeping a business core independent of HTTP and Redis.

---

## 11. Step 8 — Test helpers (tenanttest)

### 11.1 The problem solved

Before this step, to test application code depending on the current tenant, one had to write manually:

```go
t := &tenant.Tenant{
    ID:    "tenant-abc",
    State: tenant.Active,
}

ctx := tenantctx.WithTenant(context.Background(), t)
```

This logic was repeated across several tests internal to the toolkit (`fakeResolver`, `fakeStore`, `fakeAdminStore` — useful for testing the toolkit's own internal components, but not meant to be exposed). An **external user** who simply wants to test their application shouldn't have to know all this internal machinery. That is exactly the role of `tenanttest`.

### 11.2 Why a package separate from `tenantctx`

| | `tenantctx` | `tenanttest` |
|---|---|---|
| Responsibility | Manage the tenant present in the application's `context.Context` | Provide tools to easily build test contexts |
| Path | Production | Tests |

```text
Application
    │
    ▼
tenantctx.WithTenant()
    │
    ▼
context.Context
```

```text
Test
 │
 ▼
tenanttest.WithFakeTenant()
 │
 ▼
tenantctx.WithTenant()
 │
 ▼
context.Context
```

A developer importing `tenantctx` in their production application therefore never picks up, via that same import, functionality meant exclusively for testing. The packages clearly state their role: `tenantctx` = production mechanism; `tenanttest` = testing ergonomics.

### 11.3 Package architecture

```text
tenant-core/
│
├── tenantctx/
│   └── ...
│
└── tenanttest/
    ├── tenanttest.go
    └── tenanttest_test.go
```

```text
tenanttest
    │
    ├──────────────► tenant
    │
    └──────────────► tenantctx
```

No additional business logic — purely testing ergonomics.

### 11.4 `WithFakeTenant`

```go
func WithFakeTenant(
    ctx context.Context,
    id tenant.TenantID,
    state tenant.State,
) context.Context
```

```go
ctx := tenanttest.WithFakeTenant(
    context.Background(),
    "tenant-abc",
    tenant.Active,
)
```

### 11.5 Why keep a simple API

The choice was to **deliberately** keep `WithFakeTenant(ctx, id, state)` rather than progressively adding parameters to it (`roles`, `permissions`, ...), which would make the function hard to use for the most common case: *"I just need a tenant in my context."*

### 11.6 `WithFakeTenantFull`

For tests needing more control (notably RBAC):

```go
func WithFakeTenantFull(
    ctx context.Context,
    t *tenant.Tenant,
) context.Context
```

```go
ctx := tenanttest.WithFakeTenantFull(
    context.Background(),
    &tenant.Tenant{
        ID:    "tenant-admin",
        State: tenant.Active,
        Roles: []string{"admin", "manager"},
    },
)
```

Particularly useful for testing RBAC, specific roles, particular states, complex business scenarios, or future `Tenant` fields.

### 11.7 Factoring between the two helpers

`WithFakeTenant` delegates to `WithFakeTenantFull`, so the context creation/injection logic exists in only one place:

```text
                  tenanttest
                      │
             ┌────────┴────────┐
             │                 │
             ▼                 ▼
    WithFakeTenant      WithFakeTenantFull
             │                 │
             │                 │
             └────────┬────────┘
                      ▼
             tenantctx.WithTenant
                      │
                      ▼
               context.Context
```

### 11.8 Why not create a fake Resolver at this step

`tenanttest`'s current need is *injecting a tenant directly*, not *simulating the whole HTTP pipeline*. Helpers like `NewFakeResolver(...)`, `NewFakeStore(...)`, `NewFakeManager(...)` were deliberately **not** created at this step.

> **Don't abstract prematurely; start with the smallest contract that actually solves the problem.**

### 11.9 The tenanttest contract

```text
Input
  │
  ▼
tenanttest.WithFakeTenant(...)
  │
  ▼
context.Context containing the tenant
  │
  ▼
tenantctx.FromContext(ctx)
  │
  ▼
Tenant
```

> **Any tenant injected by `tenanttest` must be retrievable via the official `tenantctx.FromContext` mechanism.**

### 11.10 and 11.11 Package tests

**`TestWithFakeTenant`** verifies the minimal helper: `ID`, `State`, empty `Roles`.

**`TestWithFakeTenantFull`** verifies that a full tenant (including `Roles`) is correctly preserved — guaranteeing that RBAC information isn't lost.

Both tests remain deliberately short: `tenantctx.WithTenant()`/`FromContext()` were already tested in depth in Step 1; here, only the helper's **integration contract** is verified.

### 11.12 Usage example by a toolkit user

```go
func GetCurrentTenantName(ctx context.Context) string {
    t := tenantctx.FromContext(ctx)
    if t == nil {
        return ""
    }
    return string(t.ID)
}
```

```go
func TestGetCurrentTenantName(t *testing.T) {
    ctx := tenanttest.WithFakeTenant(
        context.Background(),
        "tenant-abc",
        tenant.Active,
    )

    got := GetCurrentTenantName(ctx)

    if got != "tenant-abc" {
        t.Fatalf("expected tenant-abc, got %s", got)
    }
}
```

The developer doesn't need to start Redis, create a `MemoryStore`, a `Resolver`, build an HTTP request, start Gin/Echo/Chi, or use `Manager`. This is exactly the benefit sought.

### 11.13 Example for RBAC

```go
ctx := tenanttest.WithFakeTenantFull(
    context.Background(),
    &tenant.Tenant{
        ID:    "tenant-abc",
        State: tenant.Active,
        Roles: []string{"admin"},
    },
)
```

Allows testing `tenant → RBAC → permission allowed/denied` with no external infrastructure whatsoever.

### 11.14 Overall architecture after Step 8

```text
                    tenant-core
                         │
       ┌─────────────────┼──────────────────┐
       │                 │                  │
       ▼                 ▼                  ▼
    Production        Adapters            Testing
       │                 │                  │
       ▼                 ▼                  ▼
    tenant          middleware          tenanttest
    tenantctx       Gin/Echo/Chi
    Manager         Redis
    Store           ...
    AdminService
```

```text
                    CORE
                     │
          ┌──────────┴──────────┐
          │                     │
          ▼                     ▼
      Production             Testing
          │                     │
          ▼                     ▼
     tenantctx             tenanttest
          │                     │
          └──────────┬──────────┘
                     ▼
               context.Context
```

### 11.15 Architectural principle adopted

> **Test tools must make the core easier to use without polluting the core with test-specific logic.**

So: `tenantctx` = production mechanism; `tenanttest` = testing ergonomics — and not `tenantctx` = production + mocks + helpers + fake stores + ....

### 11.16 Possible evolutions (not implemented)

```text
tenanttest/
│
├── tenanttest.go
├── resolver.go   (future evolution)
├── store.go      (future evolution)
├── manager.go    (future evolution)
└── ...
```

Potentially with `tenanttest.NewFakeResolver(...)`, `tenanttest.NewFakeStore(...)`, `tenanttest.NewManager(...)` — only once real needs arise, in line with the general rule of not abstracting prematurely.

### 11.17 Step 8 summary

```text
                    tenanttest
                        │
             ┌──────────┴──────────┐
             │                     │
             ▼                     ▼
     WithFakeTenant       WithFakeTenantFull
             │                     │
             └──────────┬──────────┘
                        ▼
               tenantctx.WithTenant
                        │
                        ▼
                 context.Context
                        │
                        ▼
              tenantctx.FromContext
                        │
                        ▼
                     Tenant
```

**Final goal**: let a developer easily test multi-tenant code with a fake tenant, with no infrastructure, no HTTP, no Resolver, no Store, and no framework, while using exactly the same `tenantctx` mechanism as production code.

---

## 12. Final complete architecture

```text
                         ┌─────────────────────┐
                         │     Application     │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │ Middleware Adapter  │
                         │ HTTP / Gin / Echo   │
                         │ / Chi               │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │   Tenant Manager    │
                         └──────┬────────┬─────┘
                                │        │
                         Resolver        Store
                                │        │
                                │   ┌────┴───────────┐
                                │   │ Memory / Cache  │
                                │   └────────────────┘
                                │
                                ▼
                         ┌─────────────────────┐
                         │    Tenant Context   │
                         └──────────┬──────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
                 ▼                  ▼                  ▼
              RBAC             RateLimiter          Metrics
                 │                  │                  │
                 └──────────────────┼──────────────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │     Application     │
                         └─────────────────────┘
```

**Administration:**

```text
Admin API
    │
    ▼
AdminService
    │
    ├── AdminStore
    │
    └── EventBus
          │
          ├── MemoryEventBus
          │
          └── Redis Pub/Sub
                    │
                    ▼
              Other Instances
                    │
                    ▼
                BanChecker
```

**Synthetic view of the composition (`tenant.New()`)**:

```text
                    tenant.New()
                         │
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
      Resolver         Store         EventBus
          │              │              │
          │              │        ┌─────┴─────┐
          │              │        ▼           ▼
          │              │   BanChecker     Metrics
          │              │
          │              ▼
          │         CachedStore
          │
          └──────────────┬──────────────┘
                         ▼
                    Tenant Core
```

**Why functional options** — rather than a huge constructor (`New(resolver, store, eventBus, banChecker, rateLimiter, rbac, metrics, cacheKey, ...)`), hard to read and maintain, the API adopted is:

```go
tenant.New(
    tenant.WithResolver(resolver),
    tenant.WithStore(store),
)
```

**Important**: the `Manager` actually implemented (Steps 6-7) remains deliberately minimal — it only assembles `Resolver` and `Store`, panics if either is missing, and its single method `Resolve(r *http.Request) (*Tenant, error)` stops at producing a `*Tenant`, without building a `context.Context` (to avoid an import cycle with `tenantctx`). The other components (`BanChecker`, `RateLimiter`, `RBAC`, `Metrics`, `CacheKeyer`, `EventBus`) remain **independent building blocks**, which the application invokes explicitly wherever relevant — the diagram above represents the ecosystem of available components, **not** a single pipeline automatically imposed by `Manager` itself. See the [Decisions / points to clarify](#18-decisions--points-to-clarify) section for details on this nuance between the overall vision and the actual implementation of `tenant.New()`.

---

## 13. Data flow

### 13.1 Normal user request

```text
HTTP Request
     ↓
Middleware
     ↓
Resolver
     ↓
Manager
     ↓
Store
     ↓
Tenant
     ↓
tenantctx
     ↓
Application Handler
```

In detail:

```text
Step 1 — Resolver
Request → SubdomainResolver → TenantID("tenant-a")

Step 2 — Store
TenantID("tenant-a") → Store → *Tenant{ID: tenant-a, State: active, Roles: [admin]}

Step 3 — Context
*Tenant → tenantctx.WithTenant(...) → context.Context

Components that need the tenant then call tenantctx.FromContext(ctx).
```

### 13.2 Ban

```text
Admin API
    ↓
AdminService.Ban()
    ↓
AdminStore.SetState()
    ↓
Tenant = Banned
    ↓
EventBus.Publish()
    ↓
Redis Pub/Sub
    ↓
Other instances
    ↓
BanChecker
    ↓
Local cache invalidated / state updated (timestamp-based resolution)
```

### 13.3 Application test

```text
tenanttest.WithFakeTenant(...)
          ↓
tenantctx
          ↓
Application code
          ↓
Test
```

---

## 14. Concurrency and thread-safety

Each component with shared state was analyzed according to its **access profile** (frequent reads vs. frequent writes, dynamic collection of keys vs. a single value), with the synchronization primitive matched to that specific profile — never a single mechanism applied out of habit.

### 14.1 `sync.RWMutex` — frequent reads, rare writes

Used by `MemoryStore`, `CachedStore`, `MemoryEventBus` (subscriber list), `RBAC` (role/permission definitions).

```text
Reader A ── RLock ──►
Reader B ── RLock ──►
Reader C ── RLock ──►

Writer
   │
   ▼
 Lock()
   │
   ▼
 modification
   │
   ▼
Unlock()
```

Several readers access the data simultaneously without ever blocking each other; a write remains exclusive and waits for all in-progress reads to finish.

### 14.2 The trap of pointers inside a map

```text
map[TenantID]*Tenant
```

A map protected by `RWMutex` protects access to the map **itself** (adding, removing, reading a key), but **not** the content pointed to by the values it stores, if that content is mutated directly.

```text
Map
 │
 └── *Tenant ──────────┐
                       │
                       ▼
                    Tenant
                    State
```

**Rule adopted and consistently applied**: read methods (`Get`) always return a **copy**, never the internal pointer; write methods (`SetState`, `Create`, `Update`) modify the internal object **directly, under an exclusive `Lock()`** — never through a read-modify-write round trip, which would recreate a *lost update* window.

### 14.3 `sync.Map` + `LoadOrStore` — dynamic per-key collections

Used by `BanChecker` (`TenantID → banEntry`), `TenantRateLimiter` (`TenantID → *rate.Limiter`), `MemoryMetrics` (`TenantID → *tenantMetrics`).

**The problem solved by `LoadOrStore`**: if two goroutines arrive simultaneously for a tenant that **never** yet has an entry, a plain, separate `Load` then `Store` could end up creating and overwriting two distinct values (for example two different `*rate.Limiter`s for the same tenant, one overwriting the other). `LoadOrStore` atomically guarantees that **only one** value becomes the officially shared reference, even if both goroutines each prepared their own candidate value.

```text
Goroutine A                    Goroutine B

Load → absent                  Load → absent
  │                              │
creates limiter A              creates limiter B
  │                              │
LoadOrStore                    LoadOrStore
  │                              │
A gets registered              B sees A already exists
                                  │
                           B is NOT registered

Result: both goroutines use the same *rate.Limiter A
```

### 14.4 `sync/atomic` — very frequently written counters

Used by `MemoryMetrics` for `requests`, `errors`, `latencySum`, `latencyCount` — counters incremented on every request, potentially by tens of thousands of simultaneous goroutines. `atomic.Int64.Add()` guarantees correct incrementing without ever needing an explicit lock.

**Two levels of concurrency combined** in `MemoryMetrics`: `sync.Map` protects the dynamic collection of tenants, `atomic.Int64` protects each individual counter — each at the optimal spot for its own problem.

### 14.5 Per-goroutine isolation + `recover()` — EventBus (memory and Redis)

Each handler subscribed to a `TenantEvent` runs in its **own goroutine**, individually protected by a `recover()`:

```go
defer func() {
    if r := recover(); r != nil {
        // log
    }
}()
```

**Why this is crucial**: `recover()` only works **within the same goroutine** as the `panic()` it intercepts — it must therefore be placed inside the function launched by `go`, never around the call to `Publish()` (which has already returned before the handler actually runs).

**Two levels of goroutines in `RedisEventBus`**, distinct and not to be confused:

```text
Redis listener goroutine (single, permanent)
        │
        │ sequential message reception
        ▼
   event received
        │
        ├──► handler A goroutine + recover (ephemeral, per event)
        ├──► handler B goroutine + recover
        └──► handler C goroutine + recover
```

### 14.6 Conflict resolution by timestamp — `BanChecker`

A concurrency problem more subtle than simple memory protection: an initial snapshot (loaded at startup) and an event received in parallel can both write information for the same tenant, with no guarantee on the actual execution order of their respective goroutines. The solution adopted associates each entry with a **last-updated timestamp**, and rejects any write whose timestamp is **older** than the one already stored — guaranteeing that stale information can never regress more recent information, regardless of the actual arrival order.

### 14.7 `singleflight` — deduplication of concurrent calls

Used by `CachedStore.Get()`. Distinct from the mechanisms above: this is not a **memory safety** problem (the `RWMutex` already correctly protects the cache map), but an **efficiency** problem — without `singleflight`, a spike of simultaneous requests for the same tenant on a cache miss would trigger just as many duplicate calls to the source of truth (*cache stampede*). `singleflight.Group.Do(key, fn)` guarantees that only one real call goes out to the source for a given key; concurrent callers wait and receive the same result.

### 14.8 What `go test -race` can detect

Go's race detector instruments the test binary to monitor all concurrent memory accesses. It notably detects:

- a simultaneous read and write on the same variable/field, with no shared synchronization (which would have been the case had `Get()` kept returning `MemoryStore`'s internal pointer, combined with a direct write outside the lock);
- an inconsistency in the use of an unprotected Go map under concurrent access;
- any unprotected access that *could* corrupt state, even if the test doesn't "see" an incorrect value purely by scheduling luck.

`go test -race` has been used systematically throughout every step, including in the GitHub Actions CI on every push, on the root module and on each separate Go sub-module.

---

## 15. Testability

### 15.1 General principle

Each component is designed to be tested **independently**, with no need for real infrastructure. This principle was applied from Step 1 and maintained through Step 8.

### 15.2 Internal fakes

To test the toolkit's own core components, minimal fake implementations of the interfaces (`fakeResolver`, `fakeStore`, `fakeAdminStore`, `countingStore`) are written directly inside the test files of the relevant packages — never exported publicly, they exist only to isolate the component under test from its real dependencies.

### 15.3 Pure unit tests

The vast majority of components (`tenantctx`, `store`, `eventbus`, `banchecker`, `ratelimit`, `cachekey`, `rbac`, `metrics`, `admin`) are tested with standard Go tests (`testing` + `testify`), with no external dependency.

### 15.4 Testing HTTP middlewares — `httptest`

`net/http`'s `httptest` (`httptest.NewRequest`, `httptest.NewRecorder`) is used to test the `net/http` adapter and the Chi adapter (which relies directly on `http.Handler`), simulating a real end-to-end HTTP processing chain.

### 15.5 Testing framework middlewares — framework-specific mechanisms

- **Gin** — `gin.CreateTestContext(recorder)` builds a test `*gin.Context` from an internal `*gin.Engine`.
- **Echo** — `echo.New()` + `e.NewContext(req, recorder)` builds a test `echo.Context`.
- **Chi** — relies directly on `net/http`, so the same `httptest` primitives suffice (no Chi-specific test mechanism).

In each case, the test calls the **real** handler produced by the middleware (`handler.ServeHTTP(...)`, `handler(c)`), never an internal function directly — guaranteeing that the tested behavior matches exactly what would happen in production, including the routing itself (for the Admin API in particular, using `handler.ServeHTTP()` rather than calling the handler directly also validates that the `http.ServeMux` route declarations actually work).

### 15.6 Testing Redis — `miniredis`

See [section 10.23](#1023-test-strategy--why-miniredis-rather-than-a-real-redis). A pure in-memory Redis implementation lets `RedisEventBus` be tested with no real Redis server, neither locally nor in CI.

### 15.7 `tenanttest` — testability for external users

The `tenanttest` package extends this testability principle **beyond** the toolkit itself, for developers using it in their own applications (see [Step 8](#11-step-8--test-helpers-tenanttest) in detail).

### 15.8 Concurrency tests — `go test -race`

Every component with shared state has at least one test dedicated to real concurrency (multiple simultaneous goroutines), systematically run with the `-race` flag — whether locally or in GitHub Actions CI. This is the mechanism that made it possible to discover and fix design issues (notably the shared-pointer trap in `MemoryStore`, section 14.2) before they became production bugs.

### 15.9 Why components are designed to be independently testable

Each component exposes a **minimal interface** defined in the `tenant` package (or in its own package, for components without a centralized contract yet). Any implementation, including a fake one written in a few lines inside a test file, can satisfy that contract thanks to Go's structural typing — allowing a higher-level component (`Manager`, `admin.Service`, a middleware) to be tested without ever instantiating a real database, a real Redis, or a real, full HTTP server.

---

## 16. Limitations and future evolutions

This section gathers all the limitations **explicitly documented** along the way, as well as evolutions considered but **not implemented**.

| Topic | Current state (implemented) | Known limitation | Future evolution considered |
|---|---|---|---|
| `SetState → Publish` (Admin) | Order adopted, never a lying event | Not atomic — a `Publish` can fail after a successful `SetState`, event potentially lost | Outbox pattern (single state + event transaction, asynchronous publishing worker with retry) |
| Admin API — authentication | None | The API must not be exposed directly to the Internet in production | Authentication/authorization to add |
| Admin API — HTTP errors | `writeError` always returns `500` | No `404`/`409`/`503` distinction | Fine-grained error mapping, requires an exported sentinel error at the `AdminStore` level |
| Admin API — endpoints | `Ban`/`Disable`/`Activate` only | No HTTP `Create`/`Get`, even though `AdminStore.Create` and `Store.Get` exist | Add `Service.Create`/`Service.Get` first, then the corresponding endpoints, if the business need arises |
| `EventBus` (Redis) | Functional Pub/Sub, fail-fast on subscribe, `Stop()` for clean shutdown | None — reconnection and resubscription on transient network failures are handled natively by go-redis's `*redis.PubSub` (automatic reconnect + periodic health-check ping, see §10.16); this was previously listed here as a gap based on an unverified assumption, corrected after reading the go-redis source | n/a |
| `Redis` | Real-time propagation operational | No dedicated monitoring or propagation-latency metrics (connection resilience itself is already handled by go-redis, see above) | Dedicated monitoring, propagation latency metrics |
| `MemoryStore.Get()` — copy | Shallow copy of `*Tenant` | The `Roles []string` field shares the same underlying array as the original; a consumer mutating `Roles[i]` would still affect the original | Deep copy of the `Roles` slice if this risk becomes significant |
| `RedisEventBus` tests | Covered via `miniredis` (simulation) | `miniredis` doesn't guarantee every subtlety of a real Redis server | Integration test with a real Redis, as a complement, not a replacement |
| `tenanttest` | `WithFakeTenant` / `WithFakeTenantFull` | No full HTTP pipeline simulation | `NewFakeResolver`, `NewFakeStore`, `NewFakeManager` — only if a real need arises |
| `Prometheus` (Metrics) | `MetricsCollector` interface defined + in-memory implementation | No concrete Prometheus adapter built at this stage | `PrometheusMetrics` implementation satisfying the same contract |
| Distributed `RateLimiter` | In-memory implementation (per instance) | Quotas are not shared across multiple server instances | `RedisRateLimiter`, on the same agnosticism principle as `EventBus` |
| `go.mod` — `replace` directive (sub-modules) | Used for local development before publishing | Points to a local path (`../..`), invalid for a real external user | To be removed once the root module is tagged and published |

> **Cross-cutting rule to remember**: every limitation above was **explicitly documented in the code at the moment it was identified** (comments, log messages), rather than left implicit — consistent with the project's general principle of preferring an *observable* inconsistency today over a premature false solution.

---

## 17. Package tree

```text
tenant-core/                          (root module)
├── tenant.go                         → TenantID, State, Tenant, Resolver,
│                                        Store, AdminStore, Manager, Option,
│                                        New(), Manager.Resolve()
├── tenant_test.go
│
├── tenantctx/                        → WithTenant(), FromContext()
│   └── context_test.go
│
├── resolver/                         → SubdomainResolver
│   └── subdomain_test.go
│
├── store/                            → MemoryStore, CachedStore,
│   │                                    ErrTenantNotFound
│   ├── memory.go
│   ├── memory_test.go
│   ├── cached.go
│   └── cached_test.go
│
├── eventbus/                         → EventBus, TenantEvent, MemoryEventBus
│   ├── eventbus.go
│   └── memory.go (+ tests)
│
├── banchecker/                       → BanChecker
│   └── banchecker_test.go
│
├── ratelimit/                        → TenantRateLimiter, LimitFunc
│   └── ratelimit_test.go
│
├── cachekey/                         → Key()
│   └── cachekey_test.go
│
├── rbac/                             → RBAC, DefineRole(), Can()
│   └── rbac_test.go
│
├── metrics/                          → MetricsCollector, MemoryMetrics
│   └── memory_test.go
│
├── admin/                            → Service, HTTPHandler
│   ├── admin.go
│   ├── http.go
│   └── admin_test.go / http_test.go
│
├── tenanttest/                       → WithFakeTenant(), WithFakeTenantFull()
│   └── tenanttest_test.go
│
├── middleware/
│   ├── nethttp.go                    → Wrap()                (root module)
│   │
│   ├── gin/                          → Middleware()   (SEPARATE Go sub-module)
│   │   ├── go.mod   (module .../middleware/gin, replace → ../..)
│   │   ├── gin.go
│   │   └── gin_test.go
│   │
│   ├── echo/                         → Middleware()   (SEPARATE Go sub-module)
│   │   ├── go.mod   (module .../middleware/echo, replace → ../..)
│   │   ├── echo.go
│   │   └── echo_test.go
│   │
│   └── chi/                          → Middleware()   (SEPARATE Go sub-module)
│       ├── go.mod   (module .../middleware/chi, replace → ../..)
│       ├── chi.go
│       └── chi_test.go
│
├── eventbus/redis/                   → RedisEventBus (SEPARATE Go sub-module)
│   ├── go.mod        (module .../eventbus/redis, replace → ../..)
│   ├── redis.go
│   └── redis_test.go (miniredis)
│
├── .github/workflows/ci.yml          → Multi-module CI (root + 4 sub-modules)
├── LICENSE (MIT)
├── README.md
└── .gitignore
```

**Status of the independent Go sub-modules** — `middleware/gin`, `middleware/echo`, `middleware/chi`, and `eventbus/redis` each have their **own `go.mod`**, distinct from the root module. This organization guarantees that a developer using only `net/http` (or only Gin) **never** installs the dependencies of frameworks/technologies they don't use — each sub-module builds and tests independently (`cd middleware/gin && go test ./...`), and the GitHub Actions CI runs a dedicated step per sub-module (`working-directory`), in addition to the step for the root module.

Each sub-module references the root module via a `replace ... => ../..` directive during development, allowing it to point at the local code before a tagged version is published to the public repository.

---

## 18. Decisions / points to clarify

This section flags places where the source documents describe contracts in a **conceptual way** (often introduced by the word *"Conceptually"* in the original documents), which differ slightly from the exact form adopted in the actual implementation, without this calling into question the underlying architectural decision — only the signature detail.

### 18.1 RateLimiter — conceptual interface vs. implementation adopted

Step 4's source document presents a simplified conceptual contract:

```go
type RateLimiter interface {
    Allow(ctx context.Context, id TenantID) bool
}
```

The implementation actually adopted relies on a concrete `TenantRateLimiter` type, whose `Allow` method takes a `*Tenant` directly (not just a `TenantID`), and whose per-tenant limit rule is **injected** via a function (`LimitFunc`) supplied by the application — rather than fixed in the implementation itself — built on `golang.org/x/time/rate` (a *token bucket* model). The business principle (an independent limit per tenant, an infrastructure-agnostic core) stays identical; only the exact shape of the contract differs from the conceptual version presented in the source document.

### 18.2 RBAC — conceptual `Authorizer` vs. `RBAC`/`Can` adopted

The source document presents a conceptual contract:

```go
type Authorizer interface {
    Can(t *Tenant, permission string) bool
}
```

The implementation adopted is a concrete `RBAC` type (not an interface published in `tenant.go`), with a `DefineRole(tenantID, role, permissions)` method for registration, and `Can(t *Tenant, permission string) bool` for checking — definitions being organized **per tenant** (`map[TenantID]map[role]map[permission]struct{}`), as faithfully described in the source document (section 8.5 of this document). The principle (role/permission separation, per-tenant independence, no HTTP dependency) is identical.

### 18.3 Metrics — Prometheus mentioned as done vs. actual status

The title of Step 5 in the source documents ("RBAC + Metrics (Prometheus)") and several passages describe a `PrometheusMetrics` implementation in fairly concrete terms. Based on the project's actual progress, only the **interface** `MetricsCollector` and an **in-memory** implementation (`MemoryMetrics`, with `sync.Map` + `atomic.Int64`) were actually built and tested at this stage — the Prometheus adapter itself remains a **future evolution** listed in section 16, not an already-delivered component. This distinction is made here in line with the rule *"don't turn future improvements into already-implemented features"*.

### 18.4 `tenant.New()` and full component orchestration

The source summary document (sections 11 to 16 of the *resumer.txt* document) presents an all-encompassing vision of `tenant.New()` with options like `WithEventBus`, `WithRateLimiter`, `WithRBAC`, `WithMetrics` — potentially orchestrating all nine components of the toolkit. The actual `Manager`/`New()` implementation remains deliberately **more limited**: only `Resolver` and `Store` are assembled by `New()`, with the `Resolve()` method stopping at producing a `*Tenant` (without building a `context.Context`, to avoid an import cycle with `tenantctx`). The other components (`BanChecker`, `RateLimiter`, `RBAC`, `Metrics`, `CacheKeyer`, `EventBus`) remain independent building blocks that the application or middleware adapters invoke explicitly, without being automatically chained together by `Manager` itself — consistent with the principle explicitly stated in the source document: *"this diagram represents the components available in the ecosystem, not necessarily an execution order that `tenant.New()` will automatically impose"*, and *"`tenant.New()` must stay clean: it composes dependencies; it must not become a giant middleware that mixes every responsibility"*.

### 18.5 Name of the `banchecker` package

A source document (Step 3) places `BanChecker` in a package named `banchecker/`, consistent with the rest of the documentation and with the actual implementation.

---

*End of tenant-core's complete technical documentation.*
