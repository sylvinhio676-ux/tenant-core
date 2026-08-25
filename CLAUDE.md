# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`tenant-core` is a Go multi-tenancy toolkit: it resolves which tenant an HTTP request belongs to, carries that tenant through `context.Context`, and layers cache, real-time ban propagation, rate limiting, RBAC, and metrics on top — all tenant-scoped by default. The core has zero dependency on any HTTP framework, Redis, or Prometheus; those are pluggable adapters. Full design rationale (why each decision was made, known trade-offs, concurrency proofs) lives in `docs/ARCHITECTURE.md` — read it before making non-trivial changes, especially anything touching concurrency or a public interface.

## Module layout — this is NOT a single Go module

The repo root is one Go module (`github.com/sylvinhio676-ux/tenant-core`), but four sub-directories are **separate Go modules**, each with their own `go.mod`:

- `middleware/gin`
- `middleware/echo`
- `middleware/chi`
- `eventbus/redis`

This exists so a consumer using only `net/http` (or only Gin) never pulls in Echo/Chi/Redis dependencies. Each sub-module has a `replace github.com/sylvinhio676-ux/tenant-core => ../..` (or `../../..`) directive pointing at local code — this is a development-time-only affordance and must be removed if/when a real tagged version is published and consumers should use it. `middleware/nethttp` is **not** a sub-module; it lives in the root module.

Consequence: **`go build ./...` / `go test ./...` at the repo root do not touch the four sub-modules.** They must be built/tested individually.

## Commands

```bash
# Root module — everything except the 4 sub-modules
go build ./...
go vet ./...
go test ./...
go test -race ./...
go test ./store/... -run TestCachedStore_DeduplicatesConcurrentCalls -v   # single test, any package

# Each sub-module — build/test independently, from its own directory
cd middleware/gin   && go test -race ./...
cd middleware/echo  && go test -race ./...
cd middleware/chi   && go test -race ./...
cd eventbus/redis   && go test -race ./...

# Reference demo server (nethttp adapter, MemoryStore, RBAC)
go run ./cmd/server
# curl -H "Host: acme.localhost" http://localhost:8080/api/me
```

CI (`.github/workflows/ci.yml`) runs `go vet` + `go test -race -v ./...` for the root module and for each of the four sub-modules separately (via `working-directory`) — mirror that when validating a change.

## Architecture: the "what" vs the "how"

The root package `tenant` (`tenant.go`) defines contracts only: `TenantID`, `State` (`Active`/`Disabled`/`Banned`), `Tenant{ID, State, Roles}`, the `Resolver` and `Store`/`AdminStore` interfaces, and `Manager` (assembles a `Resolver` + `Store`, panics at construction if either is missing — fail-fast). Every sub-package implements one of these contracts via Go's structural typing (no sub-package is imported by `tenant.go`, avoiding import cycles):

| Package | Implements | Notes |
|---|---|---|
| `resolver` | `tenant.Resolver` | `SubdomainResolver` today |
| `store` | `tenant.Store` + `tenant.AdminStore` | `MemoryStore` (source of truth) wrapped by `CachedStore` (TTL cache + `singleflight`) |
| `eventbus`, `eventbus/redis` | `eventbus.EventBus` | `MemoryEventBus` (single instance) / `RedisEventBus` (Pub/Sub, multi-instance) |
| `banchecker` | — | Subscribes to `EventBus`, keeps an in-memory `TenantID → banned` view for O(1) ban checks, immune to cache TTL delay |
| `ratelimit` | — | `TenantRateLimiter`, one `golang.org/x/time/rate.Limiter` per tenant via `sync.Map` |
| `cachekey` | — | `Key(tenantID, key)` — namespaces any application cache key by tenant |
| `rbac` | — | `RBAC`, permissions defined per-tenant (`map[TenantID]map[role]map[permission]struct{}`) |
| `metrics` | `metrics.MetricsCollector` | `MemoryMetrics` only; no Prometheus adapter exists yet despite being discussed in the docs |
| `middleware/{nethttp,gin,echo,chi}` | — | Each does exactly: extract request → `Manager.Resolve()` → `tenantctx.WithTenant()` → reject with 404 on error. Nothing else — no RBAC, no rate limiting, no metrics inside an adapter |
| `admin` | — | `Service` (business layer: `Ban`/`Disable`/`Activate`, each does `AdminStore.SetState()` then `EventBus.Publish()`, in that order, never atomic — see below) + `HTTPHandler` (pure `net/http`, 3 PATCH endpoints, no auth) |
| `tenanttest` | — | `WithFakeTenant`/`WithFakeTenantFull` — test-only helpers for external consumers, mirrors `tenantctx` but never imported by production code |

**`tenant.Manager.Resolve()` deliberately does not build a `context.Context`.** Doing so would require importing `tenantctx`, which imports `tenant` for the `*Tenant` type — an import cycle. Combining `Manager.Resolve()` + `tenantctx.WithTenant()` is each middleware adapter's job.

**`Store` vs `AdminStore` are separate interfaces on purpose**: `Manager` only ever needs read access and must never be able to construct a code path that bans/creates a tenant. Don't add write methods to `Store`.

**`AdminStore` intentionally has no `Ban()`/`Disable()`/`Activate()`** — only `Create`/`Update`/`SetState`. Those three business transitions live in `admin.Service` because each one must also publish an event; letting the store expose them directly would let a caller change state without publishing, causing cross-instance drift.

## Concurrency: match the primitive to the access profile (see ARCHITECTURE.md §14)

This codebase is deliberate about *which* synchronization primitive it uses, and treats a mismatch as a bug, not a style choice:

- **`sync.RWMutex`** — `MemoryStore`, `CachedStore`, `MemoryEventBus` (subscriber list), `RBAC`: frequent reads, rare writes.
- **`sync.Map` + `LoadOrStore`** — `BanChecker`, `TenantRateLimiter`, `MemoryMetrics`: dynamic per-`TenantID` collections where two goroutines racing to create the *first* entry for a tenant must converge on one shared value.
- **`sync/atomic`**: per-request counters (`MemoryMetrics`).
- **Per-goroutine `recover()`**: every `EventBus` handler runs in its own goroutine wrapped in `recover()` — a panicking handler must never affect other subscribers or the process. `recover()` only works in the same goroutine as the `panic`, so it's placed inside the `go func(){...}()`, never around `Publish()`.
- **Timestamp-based conflict resolution** in `BanChecker`: an entry write is only applied if its timestamp is >= the stored one, so a stale snapshot load can never clobber a newer event (and `Subscribe()` must be called before loading any snapshot, never after).
- **`singleflight`** in `CachedStore.Get()`: deduplicates concurrent cache-miss calls to the same tenant (not a safety fix — the mutex already prevents races — it's a stampede/efficiency fix).

**The pointer trap**: stores hold `map[TenantID]*Tenant`. `Get()` must always return a *copy*, never the internal pointer — returning the pointer lets a caller mutate shared state outside any lock. Write paths (`SetState`/`Create`/`Update`) mutate the internal object directly under `Lock()`, never via a Get-then-write-back round trip (that reopens a lost-update window). If you touch `store/memory.go`, preserve this.

Every component with shared state has a companion concurrency test run under `-race` (goroutine fan-out hammering the same key). When adding shared state, add one.

## Known, deliberately accepted gaps

Don't "fix" these without discussion — they're documented trade-offs, not oversights (full detail in `docs/ARCHITECTURE.md` §16):

- `admin.Service.transition()` does `SetState()` then `Publish()`, not atomically. This order is intentional (never publish a lying event for a state that wasn't actually persisted) but a `Publish()` failure after a successful `SetState()` can still lose the event. An Outbox pattern is the eventual fix, not yet built.
- The Admin API (`admin.HTTPHandler`) has no authentication and returns `500` for every error regardless of cause — do not expose it to untrusted networks as-is.
- `RedisEventBus` is tested against `miniredis`, not a real Redis server.
- `RateLimiter` and `Metrics` are in-memory/per-instance only; no Redis-backed distributed variant exists yet.

## Testing conventions

- Standard library `testing` + `testify` (`assert`/`require`) everywhere; no other test framework.
- Fakes (`fakeResolver`, `fakeStore`, `fakeAdminStore`, `countingStore`, etc.) are hand-written per test file, never exported, never a shared mocking library.
- HTTP adapters are tested by invoking the real handler end-to-end (`handler.ServeHTTP(...)`, `handler(c)`) via `httptest`, not by calling internal functions directly.
- `eventbus/redis` tests run against `miniredis` (in-process fake Redis) — no real Redis needed locally or in CI.
- All source comments and docs are in English — do not introduce French comments (this was a deliberate migration).
