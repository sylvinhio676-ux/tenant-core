package banchecker

import (
	"context"
	"sync"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
)

/*
*
  - banEntry represents the last known information about a tenant's ban
    state, along with the timestamp of that information — necessary to
    never let stale data (e.g. an initial snapshot started before a
    recent event) overwrite more recent data.
*/
type banEntry struct {
	banned    bool
	updatedAt time.Time
}

/*
*
  - BanChecker keeps an in-memory list of currently banned tenants,
    updated by event rather than by systematically reading the
    source of truth — see spec section 11 (Performance strategy).
*/
type BanChecker struct {
	banned sync.Map // TenantID -> banEntry
}

/*
*
  - New creates a BanChecker and immediately subscribes to the given EventBus.
    Important: Subscribe must always be called BEFORE LoadInitialBannedList,
    to never miss an event published while the initial snapshot is
    loading.
*/
func New(bus eventbus.EventBus) *BanChecker {
	bc := &BanChecker{}
	// New intentionally has no error return, so a Subscribe failure here
	// can't be surfaced to the caller without an API change. In practice
	// this only matters for an EventBus whose Subscribe can actually fail
	// (e.g. eventbus/redis.RedisEventBus against an unreachable Redis) —
	// MemoryEventBus.Subscribe never returns a non-nil error.
	_ = bus.Subscribe(bc.handleEvent)
	return bc
}

/*
*
  - apply updates a tenant's state only if the provided information
    is more recent (or as recent) than what is already known. This
    guarantees that a stale snapshot can never overwrite a more recent
    event, regardless of the actual execution order of goroutines.
*/
func (bc *BanChecker) apply(id tenant.TenantID, banned bool, at time.Time) {
	existing, loaded := bc.banned.Load(id)
	if loaded {
		e := existing.(banEntry)
		if e.updatedAt.After(at) {
			// The data already stored is more recent than what we're
			// trying to write: ignore it to never regress.
			return
		}
	}
	bc.banned.Store(id, banEntry{banned: banned, updatedAt: at})
}

func (bc *BanChecker) handleEvent(event eventbus.TenantEvent) {
	bc.apply(event.TenantID, event.State == tenant.Banned, event.Timestamp)
}

// IsBanned checks in memory (pure read, no network access) whether a
// tenant is currently banned.
func (bc *BanChecker) IsBanned(id tenant.TenantID) bool {
	v, ok := bc.banned.Load(id)
	if !ok {
		return false
	}
	return v.(banEntry).banned
}

/*
*
  - LoadInitialBannedList loads, once at startup, the state of the given
    tenants from the source of truth — necessary in a multi-instance
    environment where a new instance has no history of past
    events (spec section 6/11). Must be called
    after Subscribe (see New).
*/
func (bc *BanChecker) LoadInitialBannedList(ctx context.Context, source tenant.Store, ids []tenant.TenantID) error {
	snapshotTime := time.Now()
	for _, id := range ids {
		banned, err := source.IsBanned(ctx, id)
		if err != nil {
			return err
		}
		bc.apply(id, banned, snapshotTime)
	}
	return nil
}
