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

// fakeAdminStore simulates tenant.AdminStore to test Service in isolation.
type fakeAdminStore struct {
	tenantID    tenant.TenantID
	state       tenant.State
	setStateErr error
}

func (f *fakeAdminStore) Create(ctx context.Context, t *tenant.Tenant) error {
	return nil // unused in these tests
}

func (f *fakeAdminStore) Update(ctx context.Context, t *tenant.Tenant) error {
	return nil // unused in these tests
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

	// MemoryEventBus calls handlers asynchronously.
	events := make(chan eventbus.TenantEvent, 1)
	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	tenantID := tenant.TenantID("tenant-A")
	err = service.Ban(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify that the state was indeed changed in the Store.
	assert.Equal(t, tenantID, store.tenantID)
	assert.Equal(t, tenant.Banned, store.state)

	// Wait for the published event.
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

	// The Store's error must propagate up to the caller.
	require.Error(t, err)
	assert.ErrorIs(t, err, storeErr)

	// Since SetState failed, Publish must never be called.
	select {
	case event := <-events:
		t.Fatalf("unexpected event published: tenant=%s state=%s", event.TenantID, event.State)
	case <-time.After(100 * time.Millisecond):
		// No event: expected behavior.
	}
}
