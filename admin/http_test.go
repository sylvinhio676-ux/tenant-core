package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
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

// fakeAuthenticator simulates Authenticator to test HTTPHandler's
// authentication wiring in isolation.
type fakeAuthenticator struct {
	principal *Principal
	err       error
	calls     int
}

func (f *fakeAuthenticator) Authenticate(r *http.Request) (*Principal, error) {
	f.calls++
	return f.principal, f.err
}

func TestHTTPHandler_NoAuthenticator(t *testing.T) {
	store := &fakeAdminStore{}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)
	// No WithAuthenticator option: the Admin API must accept the
	// request without performing any authentication check.
	handler := NewHTTPHandler(service)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, tenant.TenantID("acme"), store.tenantID)
	assert.Equal(t, tenant.Banned, store.state)
}

func TestHTTPHandler_AuthenticationSuccess(t *testing.T) {
	store := &fakeAdminStore{}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)

	fake := &fakeAuthenticator{principal: &Principal{ID: "admin-user"}}
	handler := NewHTTPHandler(service, WithAuthenticator(fake))

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	// A successfully authenticated request must reach the operation.
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestHTTPHandler_AuthenticationFailure(t *testing.T) {
	store := &fakeAdminStore{}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)

	fake := &fakeAuthenticator{err: errors.New("invalid token")}
	handler := NewHTTPHandler(service, WithAuthenticator(fake))

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	// A failed authentication must be rejected before the operation runs.
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHTTPHandler_AuthenticationFailure_JSON(t *testing.T) {
	store := &fakeAdminStore{}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)

	fake := &fakeAuthenticator{err: errors.New("invalid token")}
	handler := NewHTTPHandler(service, WithAuthenticator(fake))

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)

	// Read the full response body and verify its exact JSON shape.
	body, err := io.ReadAll(recorder.Body)
	require.NoError(t, err)

	// Sanity check: the body is valid, well-formed JSON.
	var payload map[string]string
	require.NoError(t, json.Unmarshal(body, &payload))

	assert.JSONEq(t, `{"error":"unauthorized"}`, string(body))
}

func TestHTTPHandler_AuthenticationFailure_DoesNotExecuteOperation(t *testing.T) {
	// Pre-seed the store with an already-existing, active tenant.
	store := &fakeAdminStore{tenantID: "acme", state: tenant.Active}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)

	fake := &fakeAuthenticator{err: errors.New("invalid token")}
	handler := NewHTTPHandler(service, WithAuthenticator(fake))

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)

	// The operation must never have been executed: the store's state
	// must remain untouched by the (rejected) ban request.
	assert.Equal(t, tenant.Active, store.state)
	assert.Equal(t, tenant.TenantID("acme"), store.tenantID)

	// The authenticator must have been invoked exactly once.
	assert.Equal(t, 1, fake.calls)
}

func TestHTTPHandler_Ban_TenantNotFound(t *testing.T) {
	store := &fakeAdminStore{setStateErr: tenant.ErrTenantNotFound}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)
	handler := NewHTTPHandler(service)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	// A missing tenant must be reported as 404, not the generic 500.
	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.JSONEq(t, `{"error":"tenant not found"}`, recorder.Body.String())
}

func TestHTTPHandler_Ban_GenericError_Returns500(t *testing.T) {
	store := &fakeAdminStore{setStateErr: errors.New("store unavailable")}
	bus := eventbus.NewMemoryEventBus()

	service := NewAdminService(store, bus)
	handler := NewHTTPHandler(service)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/tenants/acme/ban",
		nil,
	)

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	// An unclassified error must keep falling back to 500 (non-regression).
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.JSONEq(t, `{"error":"store unavailable"}`, recorder.Body.String())
}
