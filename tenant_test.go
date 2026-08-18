package tenant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeResolver struct {
	id TenantID
}

func (f *fakeResolver) Resolve(r *http.Request) (TenantID, error) {
	return f.id, nil
}

type fakeStore struct {
	tenant *Tenant
}

func (f *fakeStore) Get(ctx context.Context, id TenantID) (*Tenant, error) {
	return f.tenant, nil
}

func (f *fakeStore) IsBanned(ctx context.Context, id TenantID) (bool, error) {
	return false, nil
}

func TestManager_Resolve(t *testing.T) {
	expected := &Tenant{
		ID:    "tenant-A",
		State: Active,
		Roles: []string{"admin"},
	}

	resolver := &fakeResolver{id: "tenant-A"}
	store := &fakeStore{tenant: expected}

	manager := New(
		WithResolver(resolver),
		WithStore(store),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	got, err := manager.Resolve(req)

	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestNew_PanicsWithoutResolver(t *testing.T) {
	store := &fakeStore{}

	assert.Panics(t, func() {
		New(WithStore(store))
	})
}

func TestNew_PanicsWithoutStore(t *testing.T) {
	resolver := &fakeResolver{id: "tenant-A"}

	assert.Panics(t, func() {
		New(WithResolver(resolver))
	})
}