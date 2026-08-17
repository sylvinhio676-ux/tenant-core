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
	}, 2) // burst de 2 — suffisant pour 2 appels immédiats côté premium

	premium := &tenant.Tenant{ID: "tenant-premium"}
	free := &tenant.Tenant{ID: "tenant-free"}

	assert.True(t, rl.Allow(premium))
	assert.True(t, rl.Allow(premium))

	// Le tenant free épuise son burst de 2 après 2 appels, puis bloqué
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

	// 50 goroutines récupèrent le limiter du même tenant en même temps
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limiters[i] = rl.getLimiter(tn)
		}(i)
	}
	wg.Wait()

	// Then : toutes les goroutines doivent avoir reçu EXACTEMENT le même
	// pointeur — preuve que LoadOrStore a bien évité la duplication
	first := limiters[0]
	for _, l := range limiters {
		assert.Same(t, first, l)
	}
}