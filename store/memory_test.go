package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestMemoryStore_GetAndSet(t *testing.T) {
	ms := NewMemoryStore()

	// Given : un tenant inexistant
	_, err := ms.Get(context.Background(), "tenant-A")
	assert.ErrorIs(t, err, ErrTenantNotFound)

	// When : on l'ajoute
	ms.set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	// Then : on doit le retrouver
	got, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	assert.Equal(t, tenant.TenantID("tenant-A"), got.ID)
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	ms := NewMemoryStore()

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := tenant.TenantID(fmt.Sprintf("tenant-%d", i))
			ms.set(&tenant.Tenant{ID: id, State: tenant.Active})
		}(i)
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := tenant.TenantID(fmt.Sprintf("tenant-%d", i))
			_, _ = ms.Get(context.Background(), id)
		}(i)
	}

	wg.Wait()

	for i := 0; i < 50; i++ {
		id := tenant.TenantID(fmt.Sprintf("tenant-%d", i))
		got, err := ms.Get(context.Background(), id)
		assert.NoError(t, err)
		assert.Equal(t, id, got.ID)
	}
}

func TestMemoryStore_GetReturnsCopyNotInternalPointer(t *testing.T) {
	ms := NewMemoryStore()
	ms.set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	// On récupère le tenant et on modifie la copie reçue
	got, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	got.State = tenant.Banned

	// Then : un second Get() ne doit PAS refléter cette modification —
	// preuve que Get() a bien retourné une copie, pas le pointeur interne
	second, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	assert.Equal(t, tenant.Active, second.State)
}

func TestMemoryStore_Create(t *testing.T) {
	ms := NewMemoryStore()

	err := ms.Create(context.Background(), &tenant.Tenant{ID: "tenant-A", State: tenant.Active})
	assert.NoError(t, err)

	// Créer le même ID une deuxième fois doit échouer
	err = ms.Create(context.Background(), &tenant.Tenant{ID: "tenant-A", State: tenant.Active})
	assert.ErrorIs(t, err, ErrTenantAlreadyExists)
}

func TestMemoryStore_Update(t *testing.T) {
	ms := NewMemoryStore()

	// Mettre à jour un tenant inexistant doit échouer
	err := ms.Update(context.Background(), &tenant.Tenant{ID: "tenant-A", State: tenant.Active})
	assert.ErrorIs(t, err, ErrTenantNotFound)

	ms.set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	err = ms.Update(context.Background(), &tenant.Tenant{ID: "tenant-A", State: tenant.Disabled, Roles: []string{"admin"}})
	assert.NoError(t, err)

	got, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	assert.Equal(t, tenant.Disabled, got.State)
	assert.Equal(t, []string{"admin"}, got.Roles)
}

func TestMemoryStore_SetState(t *testing.T) {
	ms := NewMemoryStore()

	// Modifier l'état d'un tenant inexistant doit échouer
	err := ms.SetState(context.Background(), "tenant-A", tenant.Banned)
	assert.ErrorIs(t, err, ErrTenantNotFound)

	ms.set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	err = ms.SetState(context.Background(), "tenant-A", tenant.Banned)
	assert.NoError(t, err)

	got, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	assert.Equal(t, tenant.Banned, got.State)
}

func TestMemoryStore_SetState_ConcurrentWithGet(t *testing.T) {
	ms := NewMemoryStore()
	ms.set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	var wg sync.WaitGroup

	// 100 lectures et 100 écritures d'état concurrentes sur le même tenant
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = ms.Get(context.Background(), "tenant-A")
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ms.SetState(context.Background(), "tenant-A", tenant.Banned)
		}()
	}

	wg.Wait()
	// Ce test sert surtout à être lancé sous -race : s'il n'y a pas de
	// data race, il n'y a rien de plus à vérifier explicitement ici.
}