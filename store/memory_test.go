package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	tenant "github.com/sylvinhio676-ux/tenant-core"
)

func TestMemoryStore_GetAndSet(t *testing.T) {
	ms := NewMemoryStore()

	// Given : un tenant inexistant
	_, err := ms.Get(context.Background(), "tenant-A")
	assert.ErrorIs(t, err, ErrTenantNotFound)

	// When : on l'ajoute
	ms.Set(&tenant.Tenant{ID: "tenant-A", State: tenant.Active})

	// Then : on doit le retrouver
	got, err := ms.Get(context.Background(), "tenant-A")
	assert.NoError(t, err)
	assert.Equal(t, tenant.TenantID("tenant-A"), got.ID)
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	ms := NewMemoryStore()

	var wg sync.WaitGroup

	// 50 goroutines écrivent chacune un tenant différent
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := tenant.TenantID(fmt.Sprintf("tenant-%d", i))
			ms.Set(&tenant.Tenant{ID: id, State: tenant.Active})
		}(i)
	}

	// En même temps, 50 goroutines lisent (certaines vont échouer si
	// elles lisent avant que le Set correspondant n'ait eu lieu — normal)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := tenant.TenantID(fmt.Sprintf("tenant-%d", i))
			_, _ = ms.Get(context.Background(), id) // on ne vérifie pas le résultat ici
		}(i)
	}

	wg.Wait()

	// Then : après que tout soit fini, les 50 tenants doivent tous être présents
	for i := 0; i < 50; i++ {
		id := tenant.TenantID(fmt.Sprintf("tenant-%d", i))
		got, err := ms.Get(context.Background(), id)
		assert.NoError(t, err)
		assert.Equal(t, id, got.ID)
	}
}