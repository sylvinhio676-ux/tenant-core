package store

import (
	"context"
	"errors"
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

var _ tenant.Store = (*MemoryStore)(nil)
var _ tenant.AdminStore = (*MemoryStore)(nil)

// ErrTenantNotFound is returned when a tenant does not exist in the store.
var ErrTenantNotFound = errors.New("tenant: not found")

// ErrTenantAlreadyExists is returned when attempting to create a tenant
// whose ID already exists.
var ErrTenantAlreadyExists = errors.New("tenant: already exists")

type MemoryStore struct {
	mu      sync.RWMutex
	tenants map[tenant.TenantID]*tenant.Tenant
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tenants: make(map[tenant.TenantID]*tenant.Tenant),
	}
}

// Get returns a COPY of the tenant, so the caller can never mutate the
// store's internal state through the received pointer.
func (ms *MemoryStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	t, ok := ms.tenants[id]
	if !ok {
		return nil, ErrTenantNotFound
	}

	cp := *t
	return &cp, nil
}

// set is the internal write primitive, not exposed outside the
// store package — public writes go through Create/Update/SetState.
func (ms *MemoryStore) set(t *tenant.Tenant) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	cp := *t
	ms.tenants[t.ID] = &cp
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

// Create adds a new tenant. Returns ErrTenantAlreadyExists if the ID
// is already in use.
func (ms *MemoryStore) Create(ctx context.Context, t *tenant.Tenant) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.tenants[t.ID]; exists {
		return ErrTenantAlreadyExists
	}

	cp := *t
	ms.tenants[t.ID] = &cp
	return nil
}

// Update replaces an existing tenant. Returns ErrTenantNotFound if it
// does not exist yet.
func (ms *MemoryStore) Update(ctx context.Context, t *tenant.Tenant) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.tenants[t.ID]; !exists {
		return ErrTenantNotFound
	}

	cp := *t
	ms.tenants[t.ID] = &cp
	return nil
}

// SetState only changes the state of an existing tenant, atomically
// (under an exclusive lock) to avoid any lost update between
// concurrent goroutines.
func (ms *MemoryStore) SetState(ctx context.Context, id tenant.TenantID, state tenant.State) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	t, ok := ms.tenants[id]
	if !ok {
		return ErrTenantNotFound
	}

	t.State = state
	return nil
}
