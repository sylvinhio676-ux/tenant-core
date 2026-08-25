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

	// Given: a tenant that doesn't exist
	_, err := ms.Get(context.Background(), "tenant-A")
	assert.ErrorIs(t, err, ErrTenantNotFound)

	// When: it is added
	ms.set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	// Then: it must be found
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

	// Retrieve the tenant and mutate the received copy
	got, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	got.State = tenant.Banned

	// Then: a second Get() must NOT reflect this change —
	// proof that Get() did return a copy, not the internal pointer
	second, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	assert.Equal(t, tenant.Active, second.State)
}

func TestMemoryStore_Create(t *testing.T) {
	ms := NewMemoryStore()

	err := ms.Create(context.Background(), &tenant.Tenant{ID: "tenant-A", State: tenant.Active})
	assert.NoError(t, err)

	// Creating the same ID a second time must fail
	err = ms.Create(context.Background(), &tenant.Tenant{ID: "tenant-A", State: tenant.Active})
	assert.ErrorIs(t, err, ErrTenantAlreadyExists)
}

func TestMemoryStore_Update(t *testing.T) {
	ms := NewMemoryStore()

	// Updating a tenant that doesn't exist must fail
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

	// Changing the state of a tenant that doesn't exist must fail
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

	// 100 concurrent reads and 100 concurrent state writes on the same tenant
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
	// This test mainly exists to be run with -race: if there is no
	// data race, there's nothing else to explicitly verify here.
}
