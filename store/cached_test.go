package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

// countingStore est un faux Store qui compte ses appels et simule une latence.
type countingStore struct {
	calls int64
}

func (cs *countingStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	atomic.AddInt64(&cs.calls, 1)
	time.Sleep(50 * time.Millisecond) // simule un aller-retour DB lent
	return &tenant.Tenant{ID: id, State: tenant.Active}, nil
}

func (cs *countingStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

func TestCachedStore_DeduplicatesConcurrentCalls(t *testing.T) {
	source := &countingStore{}
	cached := NewCachedStore(source, 10*time.Second)

	var wg sync.WaitGroup

	// 20 goroutines demandent TOUTES le même tenant en même temps
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cached.Get(context.Background(), "tenant-A")
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// Then : la source ne doit avoir été appelée QU'UNE SEULE FOIS,
	// pas 20 fois, malgré les 20 appels concurrents
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))
}