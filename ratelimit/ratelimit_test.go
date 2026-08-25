package ratelimit

import (
	"sync"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestTenantRateLimiter_AppliesPerTenantLimit(t *testing.T) {
	rl := NewTenantRateLimiter(func(tn *tenant.Tenant) rate.Limit {
		if tn.ID == "tenant-premium" {
			return rate.Limit(1000)
		}
		return rate.Limit(0)
	}, 2) // burst of 2 — enough for 2 immediate calls on the premium side

	premium := &tenant.Tenant{ID: "tenant-premium"}
	free := &tenant.Tenant{ID: "tenant-free"}

	assert.True(t, rl.Allow(premium))
	assert.True(t, rl.Allow(premium))

	// The free tenant exhausts its burst of 2 after 2 calls, then gets blocked
	assert.True(t, rl.Allow(free))
	assert.True(t, rl.Allow(free))
	assert.False(t, rl.Allow(free))
}

func TestTenantRateLimiter_ReusesLimiterAcrossConcurrentCalls(t *testing.T) {
	rl := NewTenantRateLimiter(func(tn *tenant.Tenant) rate.Limit {
		return rate.Limit(100)
	}, 10)

	tn := &tenant.Tenant{ID: "tenant-A"}

	var wg sync.WaitGroup
	limiters := make([]*rate.Limiter, 50)

	// 50 goroutines fetch the same tenant's limiter at the same time
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limiters[i] = rl.getLimiter(tn)
		}(i)
	}
	wg.Wait()

	// Then: every goroutine must have received EXACTLY the same
	// pointer — proof that LoadOrStore did avoid duplication
	first := limiters[0]
	for _, l := range limiters {
		assert.Same(t, first, l)
	}
}
