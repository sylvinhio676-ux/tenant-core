package banchecker

import (
	"context"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
	"github.com/stretchr/testify/assert"
)

// fakeStore est un Store minimal pour tester LoadInitialBannedList
// sans dépendre de MemoryStore ni d'une vraie base.
type fakeStore struct {
	bannedIDs map[tenant.TenantID]bool
}

func (fs *fakeStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	return nil, nil // non utilisé dans ces tests
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

	// Publish() est asynchrone (goroutines) : on laisse un court délai
	// pour que le handler ait le temps de s'exécuter avant de vérifier.
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

	// Given : un événement récent dit que le tenant N'EST PAS banni
	recentTime := time.Now()
	bc.apply("tenant-A", false, recentTime)
	assert.False(t, bc.IsBanned("tenant-A"))

	// When : un snapshot PÉRIMÉ (timestamp antérieur) arrive après,
	// et affirme que le tenant EST banni
	staleTime := recentTime.Add(-5 * time.Second)
	bc.apply("tenant-A", true, staleTime)

	// Then : l'information récente doit être préservée, le snapshot
	// périmé doit être ignoré
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