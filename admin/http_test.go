package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPHandler_Ban(t *testing.T) {
	store := &fakeAdminStore{}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)
	handler := NewHTTPHandler(service)

	events := make(chan eventbus.TenantEvent, 1)

	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/tenant-A/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	// Important: go through ServeHTTP so the ServeMux
	// actually performs the {id} matching.
	handler.ServeHTTP(recorder, req)

	// Successful ban → 204 No Content.
	assert.Equal(t, http.StatusNoContent, recorder.Code)

	// Verify that the correct tenant was passed to the Store.
	assert.Equal(t, tenant.TenantID("tenant-A"), store.tenantID)
	assert.Equal(t, tenant.Banned, store.state)

	// Verify that the event was indeed published.
	select {
	case event := <-events:
		assert.Equal(t, tenant.TenantID("tenant-A"), event.TenantID)
		assert.Equal(t, tenant.Banned, event.State)
		assert.False(t, event.Timestamp.IsZero())

	case <-time.After(time.Second):
		t.Fatal("timeout: expected tenant event was not received")
	}
}
