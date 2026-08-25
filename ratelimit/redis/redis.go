// Package redis provides a distributed, Redis-backed implementation of
// ratelimit.RateLimiter.
package redis

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/ratelimit"
)

var _ ratelimit.RateLimiter = (*RedisRateLimiter)(nil)

// defaultCommandTimeout bounds how long Allow waits on Redis before
// treating the call as failed and applying the configured FailurePolicy.
const defaultCommandTimeout = 200 * time.Millisecond

// FailurePolicy decides what Allow returns when Redis itself cannot be
// reached to evaluate a tenant's quota.
type FailurePolicy int

const (
	// FailOpen allows the request when Redis is unavailable, trading
	// strict quota enforcement for availability. This is the default.
	FailOpen FailurePolicy = iota
	// FailClosed denies the request when Redis is unavailable, trading
	// availability for strict quota enforcement.
	FailClosed
)

func (p FailurePolicy) String() string {
	switch p {
	case FailOpen:
		return "fail-open"
	case FailClosed:
		return "fail-closed"
	default:
		return "unknown"
	}
}

// LimitFunc determines the request quota applicable to a given tenant:
// at most limit requests per window. Injected from the application, so
// RedisRateLimiter stays independent of business logic (plans,
// subscriptions...) — same principle as ratelimit.LimitFunc.
type LimitFunc func(t *tenant.Tenant) (limit int, window time.Duration)

/*
 * RedisRateLimiter is a distributed alternative to
 * ratelimit.TenantRateLimiter: it enforces a per-tenant request quota
 * shared across every instance of the application via Redis, instead of
 * being local to a single process.
 *
 * Algorithm — fixed window counter, NOT a sliding window or a true token
 * bucket: the current window is identified by truncating the current
 * time to a multiple of the window duration, and a Redis key
 * ("ratelimit:<tenant_id>:<window_start_unix_nano>") counts requests seen
 * within it. A single Lua script atomically increments that counter and
 * assigns it a TTL (on its first increment, so the key expires on its
 * own once the window ends) and checks it against the limit in one
 * round-trip, so the increment and the limit check can never race across
 * concurrent callers or instances.
 *
 * Known, accepted limitation of a fixed window counter: a tenant can
 * legally send up to `limit` requests right before a window boundary and
 * another `limit` right after it, so in the worst case roughly 2x the
 * configured limit can go through within a short span straddling two
 * windows. This is a deliberate V1 trade-off (one round-trip, simple to
 * reason about, no extra state) — not a full distributed token bucket.
 *
 * Prefer ratelimit.TenantRateLimiter (local, no network round-trip, no
 * infrastructure dependency) unless a quota genuinely needs to be shared
 * across instances — see docs/ARCHITECTURE.md §7 for the full trade-off.
 */
type RedisRateLimiter struct {
	client        *redis.Client
	limitFunc     LimitFunc
	failurePolicy FailurePolicy
}

// Option configures a RedisRateLimiter at creation time.
type Option func(*RedisRateLimiter)

// WithFailurePolicy sets the behavior when Redis cannot be reached to
// evaluate a request. The default, if this option is not used, is
// FailOpen.
func WithFailurePolicy(p FailurePolicy) Option {
	return func(rl *RedisRateLimiter) {
		rl.failurePolicy = p
	}
}

// New creates a RedisRateLimiter using client to store quota counters,
// with per-tenant limits determined by limitFunc.
func New(client *redis.Client, limitFunc LimitFunc, opts ...Option) *RedisRateLimiter {
	rl := &RedisRateLimiter{
		client:        client,
		limitFunc:     limitFunc,
		failurePolicy: FailOpen,
	}
	for _, opt := range opts {
		opt(rl)
	}
	return rl
}

// allowScript atomically increments the counter for a window, sets its
// TTL on first use, and reports whether the limit was exceeded — all in
// a single round-trip, so concurrent callers (from any instance) can
// never race between the increment and the limit check.
//
// KEYS[1] = the window's Redis key
// ARGV[1] = window duration in milliseconds (for PEXPIRE)
// ARGV[2] = the tenant's limit for this window
var allowScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
if current > tonumber(ARGV[2]) then
	return 0
else
	return 1
end
`)

// Allow reports whether a request for t is allowed under its quota, as
// tracked in Redis and shared across every RedisRateLimiter instance
// pointed at the same Redis server and key namespace.
//
// If Redis cannot be reached within defaultCommandTimeout, Allow logs a
// warning and falls back to the configured FailurePolicy instead of
// blocking indefinitely or panicking.
func (rl *RedisRateLimiter) Allow(t *tenant.Tenant) bool {
	limit, window := rl.limitFunc(t)

	windowStart := time.Now().Truncate(window)
	key := fmt.Sprintf("ratelimit:%s:%d", t.ID, windowStart.UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), defaultCommandTimeout)
	defer cancel()

	result, err := allowScript.Run(ctx, rl.client, []string{key}, window.Milliseconds(), limit).Int()
	if err != nil {
		log.Printf(
			"WARNING ratelimit/redis: Redis unavailable, applying failure policy: tenant_id=%s policy=%s error=%v",
			t.ID, rl.failurePolicy, err,
		)
		return rl.failurePolicy == FailOpen
	}

	return result == 1
}
