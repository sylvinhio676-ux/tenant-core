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

// fakeResolver simule le Resolver.
type fakeResolver struct {
	id  tenant.TenantID
	err error
}

func (f *fakeResolver) Resolve(r *http.Request) (tenant.TenantID, error) {
	return f.id, f.err
}

// fakeStore simule le Store.
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

	// Handler final qui vérifie que le tenant est bien présent
	// dans le contexte reçu.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualTenant := tenantctx.FromContext(r.Context())

		require.NotNil(t, actualTenant)

		assert.Equal(t, expectedTenant.ID, actualTenant.ID)
		assert.Equal(t, expectedTenant.State, actualTenant.State)
		assert.Equal(t, expectedTenant.Roles, actualTenant.Roles)

		w.WriteHeader(http.StatusOK)
	})

	// Construction du middleware Chi.
	handler := Middleware(manager)(next)

	// Exécution de la chaîne HTTP.
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
}