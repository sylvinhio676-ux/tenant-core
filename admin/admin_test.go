package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminStore simule tenant.AdminStore pour tester Service isolément.
type fakeAdminStore struct {
	tenantID    tenant.TenantID
	state       tenant.State
	setStateErr error
}

func (f *fakeAdminStore) Create(ctx context.Context, t *tenant.Tenant) error {
	return nil // non utilisé dans ces tests
}

func (f *fakeAdminStore) Update(ctx context.Context, t *tenant.Tenant) error {
	return nil // non utilisé dans ces tests
}

func (f *fakeAdminStore) SetState(ctx context.Context, id tenant.TenantID, state tenant.State) error {
	if f.setStateErr != nil {
		return f.setStateErr
	}
	f.tenantID = id
	f.state = state
	return nil
}

func TestService_Ban_UpdatesStateAndPublishesEvent(t *testing.T) {
	store := &fakeAdminStore{}
	bus := eventbus.NewMemoryEventBus()
	service := NewAdminService(store, bus)

	// Le MemoryEventBus appelle les handlers de manière asynchrone.
	events := make(chan eventbus.TenantEvent, 1)
	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	tenantID := tenant.TenantID("tenant-A")
	err = service.Ban(context.Background(), tenantID)
	require.NoError(t, err)

	// Vérifie que l'état a bien été modifié dans le Store.
	assert.Equal(t, tenantID, store.tenantID)
	assert.Equal(t, tenant.Banned, store.state)

	// Attend l'événement publié.
	select {
	case event := <-events:
		assert.Equal(t, tenantID, event.TenantID)
		assert.Equal(t, tenant.Banned, event.State)
		assert.False(t, event.Timestamp.IsZero())
	case <-time.After(time.Second):
		t.Fatal("timeout: expected tenant event was not received")
	}
}

func TestService_Ban_DoesNotPublishWhenStoreFails(t *testing.T) {
	storeErr := errors.New("store unavailable")
	store := &fakeAdminStore{setStateErr: storeErr}
	bus := eventbus.NewMemoryEventBus()
	service := NewAdminService(store, bus)

	events := make(chan eventbus.TenantEvent, 1)
	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	tenantID := tenant.TenantID("tenant-A")
	err = service.Ban(context.Background(), tenantID)

	// L'erreur du Store doit remonter jusqu'à l'appelant.
	require.Error(t, err)
	assert.ErrorIs(t, err, storeErr)

	// Puisque SetState a échoué, Publish ne doit jamais être appelé.
	select {
	case event := <-events:
		t.Fatalf("unexpected event published: tenant=%s state=%s", event.TenantID, event.State)
	case <-time.After(100 * time.Millisecond):
		// Aucun événement : comportement attendu.
	}
}