package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// benchStore est un Store minimal pour les benchmarks : pas de latence
// artificielle, juste de quoi satisfaire l'interface.
type benchStore struct{}

func (bs *benchStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	return &tenant.Tenant{ID: id, State: tenant.Active}, nil
}

func (bs *benchStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

func BenchmarkCachedStore_CacheHit(b *testing.B) {
	tenantCounts := []int{1, 10, 100, 1000}

	for _, count := range tenantCounts {
		b.Run(fmt.Sprintf("%d_tenants", count), func(b *testing.B) {
			cs := NewCachedStore(&benchStore{}, 10*time.Second)

			// Préparation : on remplit le cache pour tous les tenants
			// AVANT de démarrer la mesure (b.ResetTimer exclut cette
			// préparation du chronométrage).
			ids := make([]tenant.TenantID, count)
			for i := 0; i < count; i++ {
				ids[i] = tenant.TenantID(fmt.Sprintf("tenant-%d", i))
				_, _ = cs.Get(context.Background(), ids[i])
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				id := ids[i%count]
				_, _ = cs.Get(context.Background(), id)
			}
		})
	}
}

func BenchmarkCachedStore_CacheMiss(b *testing.B) {
	cs := NewCachedStore(&benchStore{}, 10*time.Second)

	// IDs uniques pré-générés, jamais présents dans le cache — garantit
	// un miss à chaque itération, sans polluer la mesure avec le coût
	// de fmt.Sprintf.
	ids := make([]tenant.TenantID, b.N)
	for i := 0; i < b.N; i++ {
		ids[i] = tenant.TenantID(fmt.Sprintf("tenant-%d", i))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cs.Get(context.Background(), ids[i])
	}
}

func BenchmarkCachedStore_ConcurrentMiss_SameTenant(b *testing.B) {
	source := &countingBenchStore{}
	cs := NewCachedStore(source, 10*time.Second)

	id := tenant.TenantID("tenant-A")

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cs.Get(context.Background(), id)
		}
	})

	b.ReportMetric(float64(source.calls.Load()), "source_calls")
}

// countingBenchStore compte ses appels réels, pour vérifier que
// singleflight déduplique bien les accès concurrents.
type countingBenchStore struct {
	calls atomic.Int64
}

func (cs *countingBenchStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	cs.calls.Add(1)
	return &tenant.Tenant{ID: id, State: tenant.Active}, nil
}

func (cs *countingBenchStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

// slowBenchStore simule une latence réelle (ex: DB), pour créer
// volontairement une fenêtre de contention où plusieurs goroutines
// arrivent AVANT que le premier appel n'ait eu le temps de terminer.
type slowBenchStore struct {
	calls atomic.Int64
	delay time.Duration
}

func (s *slowBenchStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	s.calls.Add(1)
	time.Sleep(s.delay)
	return &tenant.Tenant{ID: id, State: tenant.Active}, nil
}

func (s *slowBenchStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

func BenchmarkCachedStore_SingleflightDeduplication(b *testing.B) {
	concurrencyLevels := []int{10, 50, 200}

	for _, n := range concurrencyLevels {
		b.Run(fmt.Sprintf("%d_concurrent", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				source := &slowBenchStore{delay: 20 * time.Millisecond}
				cs := NewCachedStore(source, 10*time.Second)
				id := tenant.TenantID("tenant-A")

				var wg sync.WaitGroup
				for g := 0; g < n; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, _ = cs.Get(context.Background(), id)
					}()
				}
				wg.Wait()

				if calls := source.calls.Load(); calls != 1 {
					b.Fatalf("expected exactly 1 source call, got %d", calls)
				}
			}
		})
	}
}