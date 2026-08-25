package echo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	echo "github.com/labstack/echo/v4"

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

	// Set up the Echo test environment.
	e := echo.New()

	req := httptest.NewRequest(
		http.MethodGet,
		"/",
		nil,
	)

	recorder := httptest.NewRecorder()

	c := e.NewContext(req, recorder)

	// Build the middleware with a fake "next".
	middleware := Middleware(manager)

	handler := middleware(func(c echo.Context) error {
		return nil
	})

	// Run the middleware.
	err := handler(c)

	require.NoError(t, err)

	// Verify that the tenant was injected into the request's
	// standard context.
	actualTenant := tenantctx.FromContext(
		c.Request().Context(),
	)

	require.NotNil(t, actualTenant)

	assert.Equal(t, expectedTenant.ID, actualTenant.ID)
	assert.Equal(t, expectedTenant.State, actualTenant.State)
	assert.Equal(t, expectedTenant.Roles, actualTenant.Roles)
}
