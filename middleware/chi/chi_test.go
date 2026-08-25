package chi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver simulates the Resolver.
type fakeResolver struct {
	id  tenant.TenantID
	err error
}

func (f *fakeResolver) Resolve(r *http.Request) (tenant.TenantID, error) {
	return f.id, f.err
}

// fakeStore simulates the Store.
type fakeStore struct {
	tenant *tenant.Tenant
	err    error
}

func (f *fakeStore) Get(
	ctx context.Context,
	id tenant.TenantID,
) (*tenant.Tenant, error) {
	return f.tenant, f.err
}

func (f *fakeStore) IsBanned(
	ctx context.Context,
	id tenant.TenantID,
) (bool, error) {
	return false, nil
}

func TestMiddleware_InjectsTenantIntoContext(t *testing.T) {
	expectedTenant := &tenant.Tenant{
		ID:    "tenant-A",
		State: tenant.Active,
		Roles: []string{"admin"},
	}

	manager := tenant.New(
		tenant.WithResolver(&fakeResolver{
			id: "tenant-A",
		}),
		tenant.WithStore(&fakeStore{
			tenant: expectedTenant,
		}),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/",
		nil,
	)

	recorder := httptest.NewRecorder()

	// Final handler that verifies the tenant is indeed present
	// in the received context.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualTenant := tenantctx.FromContext(r.Context())

		require.NotNil(t, actualTenant)

		assert.Equal(t, expectedTenant.ID, actualTenant.ID)
		assert.Equal(t, expectedTenant.State, actualTenant.State)
		assert.Equal(t, expectedTenant.Roles, actualTenant.Roles)

		w.WriteHeader(http.StatusOK)
	})

	// Build the Chi middleware.
	handler := Middleware(manager)(next)

	// Run the HTTP chain.
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
}
