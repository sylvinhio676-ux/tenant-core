package store

import (
	"context"
	"errors"
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

var ErrTenantNotFound = errors.New("tenant: not found")

type MemoryStore struct {
	mu      sync.RWMutex
	tenants map[tenant.TenantID]*tenant.Tenant
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tenants: make(map[tenant.TenantID]*tenant.Tenant),
	}
}

func (ms *MemoryStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	t, ok := ms.tenants[id]
	if !ok {
		return nil, ErrTenantNotFound
	}

	return t, nil
}

func (ms *MemoryStore) Set(t *tenant.Tenant) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.tenants[t.ID] = t
}

func (ms *MemoryStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	t, ok := ms.tenants[id]
	if !ok {
		return false, ErrTenantNotFound
	}

	return t.State == tenant.Banned, nil
}