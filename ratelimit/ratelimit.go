package ratelimit

import (
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"golang.org/x/time/rate"
)

// RateLimiter decides whether a request for a given tenant is allowed
// right now. Implementations differ in where the quota state lives —
// TenantRateLimiter keeps it in local process memory; ratelimit/redis's
// RedisRateLimiter shares it across instances via Redis — but every
// caller only ever needs this one method.
type RateLimiter interface {
	Allow(t *tenant.Tenant) bool
}

// LimitFunc determines the requests-per-second limit applicable to a
// given tenant. Injected from the application, so TenantRateLimiter
// stays entirely independent of business logic (plans, subscriptions...).
type LimitFunc func(t *tenant.Tenant) rate.Limit

// TenantRateLimiter applies request quotas that differ per tenant.
type TenantRateLimiter struct {
	limitFunc LimitFunc
	burst     int
	limiters  sync.Map // TenantID -> *rate.Limiter
}

var _ RateLimiter = (*TenantRateLimiter)(nil)

// NewTenantRateLimiter creates a TenantRateLimiter. burst is the allowed
// burst size, identical for all tenants (simplification for
// now — could become tenant-specific later if needed).
func NewTenantRateLimiter(limitFunc LimitFunc, burst int) *TenantRateLimiter {
	return &TenantRateLimiter{
		limitFunc: limitFunc,
		burst:     burst,
	}
}

// Allow indicates whether a request for this tenant is allowed right now,
// based on its quota. Creates the tenant's *rate.Limiter on first encounter.
func (rl *TenantRateLimiter) Allow(t *tenant.Tenant) bool {
	limiter := rl.getLimiter(t)
	return limiter.Allow()
}

// getLimiter returns the tenant's *rate.Limiter, creating it if it
// doesn't exist yet.
//
// Known and measured trade-off (see ratelimit/loadorstore_bench_test.go):
// LoadOrStore guarantees that only one *rate.Limiter is ultimately stored per
// tenant, but N goroutines that simultaneously encounter a NEW
// tenant each build their own candidate before deduplication
// (N candidates created, only 1 kept — confirmed experimentally).
// This waste only happens on a tenant's very first initialization,
// never on the hot path (0 allocations, benchmarked stable from
// 1 to 1000 tenants). Accepted for V1 to keep the hot path
// free of an explicit lock; to be reevaluated if a real production
// profile shows significant GC pressure from massive creation of
// new tenants.

func (rl *TenantRateLimiter) getLimiter(t *tenant.Tenant) *rate.Limiter {
	if v, ok := rl.limiters.Load(t.ID); ok {
		return v.(*rate.Limiter)
	}

	limit := rl.limitFunc(t)
	newLimiter := rate.NewLimiter(limit, rl.burst)

	actual, _ := rl.limiters.LoadOrStore(t.ID, newLimiter)
	return actual.(*rate.Limiter)
}
