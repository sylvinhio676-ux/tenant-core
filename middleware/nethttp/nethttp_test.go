package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

type fakeResolver struct {
	id  tenant.TenantID
	err error
}

func (f *fakeResolver) Resolve(r *http.Request) (tenant.TenantID, error) {
	return f.id, f.err
}

type fakeStore struct {
	tenant *tenant.Tenant
}

func (f *fakeStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	return f.tenant, nil
}

func (f *fakeStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

func TestWrap_InjectsTenantIntoContext(t *testing.T) {
	expected := &tenant.Tenant{ID: "tenant-A", State: tenant.Active}

	manager := tenant.New(
		tenant.WithResolver(&fakeResolver{id: "tenant-A"}),
		tenant.WithStore(&fakeStore{tenant: expected}),
	)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := tenantctx.FromContext(r.Context())
		if got == nil {
			http.Error(w, "tenant absent", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(string(got.ID)))
	})

	handler := Wrap(manager, next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "tenant-A", recorder.Body.String())
}

func TestWrap_RejectsWhenResolverFails(t *testing.T) {
	manager := tenant.New(
		tenant.WithResolver(&fakeResolver{err: assert.AnError}),
		tenant.WithStore(&fakeStore{}),
	)

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := Wrap(manager, next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.False(t, nextCalled)
}
