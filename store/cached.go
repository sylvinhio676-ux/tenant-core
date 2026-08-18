package store

import (
	"context"
	"sync"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"golang.org/x/sync/singleflight"
)

type cacheEntry struct {
	tenant    *tenant.Tenant
	expiresAt time.Time
}

type CachedStore struct {
	source tenant.Store
	ttl    time.Duration

	mu    sync.RWMutex
	cache map[tenant.TenantID]cacheEntry

	group singleflight.Group
}

func NewCachedStore(source tenant.Store, ttl time.Duration) *CachedStore {
	return &CachedStore{
		source: source,
		ttl:    ttl,
		cache:  make(map[tenant.TenantID]cacheEntry),
	}
}

func (cs *CachedStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	cs.mu.RLock()
	entry, found := cs.cache[id]
	cs.mu.RUnlock()

	if found && time.Now().Before(entry.expiresAt) {
		return entry.tenant, nil
	}

	v, err, _ := cs.group.Do(string(id), func() (interface{}, error) {
		t, err := cs.source.Get(ctx, id)
		if err != nil {
			return nil, err
		}

		cs.mu.Lock()
		cs.cache[id] = cacheEntry{tenant: t, expiresAt: time.Now().Add(cs.ttl)}
		cs.mu.Unlock()

		return t, nil
	})

	if err != nil {
		return nil, err
	}
	return v.(*tenant.Tenant), nil
}

func (cs *CachedStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return cs.source.IsBanned(ctx, id)
}