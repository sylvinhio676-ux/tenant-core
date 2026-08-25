package banchecker

import (
	"context"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
	"github.com/stretchr/testify/assert"
)

// fakeStore is a minimal Store for testing LoadInitialBannedList
// without depending on MemoryStore or a real database.
type fakeStore struct {
	bannedIDs map[tenant.TenantID]bool
}

func (fs *fakeStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	return nil, nil // unused in these tests
}

func (fs *fakeStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return fs.bannedIDs[id], nil
}

func TestBanChecker_HandlesEventDirectly(t *testing.T) {
	bus := eventbus.NewMemoryEventBus()
	bc := New(bus)

	assert.False(t, bc.IsBanned("tenant-A"))

	bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID:  "tenant-A",
		State:     tenant.Banned,
		Timestamp: time.Now(),
	})

	// Publish() is asynchronous (goroutines): give it a short delay
	// so the handler has time to run before checking.
	time.Sleep(20 * time.Millisecond)

	assert.True(t, bc.IsBanned("tenant-A"))
}

func TestBanChecker_UnbanRemovesFromBannedList(t *testing.T) {
	bus := eventbus.NewMemoryEventBus()
	bc := New(bus)

	bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now(),
	})
	time.Sleep(20 * time.Millisecond)
	assert.True(t, bc.IsBanned("tenant-A"))

	bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID: "tenant-A", State: tenant.Active, Timestamp: time.Now(),
	})
	time.Sleep(20 * time.Millisecond)
	assert.False(t, bc.IsBanned("tenant-A"))
}

func TestBanChecker_StaleSnapshotNeverOverridesRecentEvent(t *testing.T) {
	bus := eventbus.NewMemoryEventBus()
	bc := New(bus)

	// Given: a recent event says the tenant is NOT banned
	recentTime := time.Now()
	bc.apply("tenant-A", false, recentTime)
	assert.False(t, bc.IsBanned("tenant-A"))

	// When: a STALE snapshot (earlier timestamp) arrives afterwards,
	// and claims the tenant IS banned
	staleTime := recentTime.Add(-5 * time.Second)
	bc.apply("tenant-A", true, staleTime)

	// Then: the recent information must be preserved, the stale
	// snapshot must be ignored
	assert.False(t, bc.IsBanned("tenant-A"))
}

func TestBanChecker_LoadInitialBannedList(t *testing.T) {
	bus := eventbus.NewMemoryEventBus()
	bc := New(bus)

	source := &fakeStore{
		bannedIDs: map[tenant.TenantID]bool{
			"tenant-A": true,
			"tenant-B": false,
		},
	}

	err := bc.LoadInitialBannedList(context.Background(), source,
		[]tenant.TenantID{"tenant-A", "tenant-B"})

	assert.NoError(t, err)
	assert.True(t, bc.IsBanned("tenant-A"))
	assert.False(t, bc.IsBanned("tenant-B"))
}
