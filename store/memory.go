package store

import (
	"context"
	"errors"
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

var _ tenant.Store = (*MemoryStore)(nil)
var _ tenant.AdminStore = (*MemoryStore)(nil)

// ErrTenantNotFound est retournée quand un tenant n'existe pas dans le store.
var ErrTenantNotFound = errors.New("tenant: not found")

// ErrTenantAlreadyExists est retournée quand on tente de créer un tenant
// dont l'ID existe déjà.
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

// Get retourne une COPIE du tenant, pour que le consommateur ne puisse
// jamais muter l'état interne du store via le pointeur reçu.
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

// set est la primitive interne d'écriture, non exposée en dehors du
// package store — les écritures publiques passent par Create/Update/SetState.
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

// Create ajoute un nouveau tenant. Retourne ErrTenantAlreadyExists si l'ID
// est déjà utilisé.
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

// Update remplace un tenant existant. Retourne ErrTenantNotFound s'il
// n'existe pas encore.
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

// SetState modifie uniquement l'état d'un tenant existant, de façon
// atomique (sous verrou exclusif) pour éviter tout lost update entre
// goroutines concurrentes.
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