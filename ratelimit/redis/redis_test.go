package redis

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// fixedLimit returns a LimitFunc that always applies the same limit and
// window, regardless of the tenant — enough for these tests, which
// isolate tenants by ID rather than by quota configuration.
func fixedLimit(limit int, window time.Duration) LimitFunc {
	return func(t *tenant.Tenant) (int, time.Duration) {
		return limit, window
	}
}

func TestRedisRateLimiter_AllowsWithinLimit(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	rl := New(client, fixedLimit(5, time.Minute))

	tn := &tenant.Tenant{ID: "tenant-A"}
	for i := 0; i < 5; i++ {
		assert.True(t, rl.Allow(tn), "request %d should be allowed", i+1)
	}
}

func TestRedisRateLimiter_DeniesOverLimit(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	rl := New(client, fixedLimit(5, time.Minute))

	tn := &tenant.Tenant{ID: "tenant-A"}
	for i := 0; i < 5; i++ {
		require.True(t, rl.Allow(tn))
	}

	assert.False(t, rl.Allow(tn), "the 6th request within the same window must be denied")
}

func TestRedisRateLimiter_SeparateTenantsIndependent(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	rl := New(client, fixedLimit(2, time.Minute))

	tenantA := &tenant.Tenant{ID: "tenant-A"}
	tenantB := &tenant.Tenant{ID: "tenant-B"}

	assert.True(t, rl.Allow(tenantA))
	assert.True(t, rl.Allow(tenantA))
	assert.False(t, rl.Allow(tenantA), "tenant-A must have exhausted its own quota")

	// tenant-B has its own, completely untouched quota.
	assert.True(t, rl.Allow(tenantB))
	assert.True(t, rl.Allow(tenantB))
	assert.False(t, rl.Allow(tenantB), "tenant-B must have exhausted its own quota, independently of tenant-A")
}

func TestRedisRateLimiter_FailOpen(t *testing.T) {
	// Point at an address with no Redis server at all, so every call fails.
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	rl := New(client, fixedLimit(5, time.Minute), WithFailurePolicy(FailOpen))

	tn := &tenant.Tenant{ID: "tenant-A"}
	assert.True(t, rl.Allow(tn), "FailOpen must allow requests when Redis is unreachable")
}

func TestRedisRateLimiter_FailClosed(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	rl := New(client, fixedLimit(5, time.Minute), WithFailurePolicy(FailClosed))

	tn := &tenant.Tenant{ID: "tenant-A"}
	assert.False(t, rl.Allow(tn), "FailClosed must deny requests when Redis is unreachable")
}

func TestRedisRateLimiter_SharedAcrossMultipleClients(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Two independent RedisRateLimiter instances, simulating two separate
	// application instances, both pointed at the same Redis server.
	client1 := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	client2 := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})

	rl1 := New(client1, fixedLimit(3, time.Minute))
	rl2 := New(client2, fixedLimit(3, time.Minute))

	tn := &tenant.Tenant{ID: "tenant-A"}

	// The quota is shared: 3 total across both instances, not 3 each —
	// proof this is genuinely distributed, not just correct in isolation.
	assert.True(t, rl1.Allow(tn))
	assert.True(t, rl2.Allow(tn))
	assert.True(t, rl1.Allow(tn))

	assert.False(t, rl2.Allow(tn), "the 4th request across both instances must be denied")
}
