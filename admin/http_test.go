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

	// Important : on passe par ServeHTTP pour que le ServeMux
	// effectue réellement le matching de {id}.
	handler.ServeHTTP(recorder, req)

	// Ban réussi → 204 No Content.
	assert.Equal(t, http.StatusNoContent, recorder.Code)

	// Vérifie que le bon tenant a été transmis au Store.
	assert.Equal(t, tenant.TenantID("tenant-A"), store.tenantID)
	assert.Equal(t, tenant.Banned, store.state)

	// Vérifie que l'événement a bien été publié.
	select {
	case event := <-events:
		assert.Equal(t, tenant.TenantID("tenant-A"), event.TenantID)
		assert.Equal(t, tenant.Banned, event.State)
		assert.False(t, event.Timestamp.IsZero())

	case <-time.After(time.Second):
		t.Fatal("timeout: expected tenant event was not received")
	}
}