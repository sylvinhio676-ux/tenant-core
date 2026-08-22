package banchecker

import (
	"fmt"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
)

func BenchmarkBanChecker_IsBanned_Warm(b *testing.B) {
	tenantCounts := []int{1, 10, 100, 1000, 10000}

	for _, count := range tenantCounts {
		b.Run(fmt.Sprintf("%d_tenants", count), func(b *testing.B) {
			bus := eventbus.NewMemoryEventBus()
			bc := New(bus)

			ids := make([]tenant.TenantID, count)
			for i := 0; i < count; i++ {
				ids[i] = tenant.TenantID(fmt.Sprintf("tenant-%d", i))
				// Moitié bannis, moitié actifs, pour un scénario réaliste
				state := tenant.Active
				if i%2 == 0 {
					state = tenant.Banned
				}
				bc.apply(ids[i], state == tenant.Banned, time.Now())
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				bc.IsBanned(ids[i%count])
			}
		})
	}
}

func BenchmarkBanChecker_IsBanned_ConcurrentWithApply(b *testing.B) {
	bus := eventbus.NewMemoryEventBus()
	bc := New(bus)

	const tenantCount = 100
	ids := make([]tenant.TenantID, tenantCount)
	for i := 0; i < tenantCount; i++ {
		ids[i] = tenant.TenantID(fmt.Sprintf("tenant-%d", i))
		bc.apply(ids[i], false, time.Now())
	}

	// Une goroutine dédiée écrit en continu pendant toute la durée du
	// benchmark, simulant des changements d'état réguliers en arrière-plan.
	stop := make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				id := ids[i%tenantCount]
				bc.apply(id, i%2 == 0, time.Now())
				i++
			}
		}
	}()
	defer close(stop)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			bc.IsBanned(ids[i%tenantCount])
			i++
		}
	})
}