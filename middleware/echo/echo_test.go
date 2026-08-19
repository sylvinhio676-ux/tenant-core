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

	// Création de l'environnement de test Echo.
	e := echo.New()

	req := httptest.NewRequest(
		http.MethodGet,
		"/",
		nil,
	)

	recorder := httptest.NewRecorder()

	c := e.NewContext(req, recorder)

	// Construction du middleware avec un "next" factice.
	middleware := Middleware(manager)

	handler := middleware(func(c echo.Context) error {
		return nil
	})

	// Exécution du middleware.
	err := handler(c)

	require.NoError(t, err)

	// Vérifie que le tenant a été injecté dans le contexte
	// standard de la requête.
	actualTenant := tenantctx.FromContext(
		c.Request().Context(),
	)

	require.NotNil(t, actualTenant)

	assert.Equal(t, expectedTenant.ID, actualTenant.ID)
	assert.Equal(t, expectedTenant.State, actualTenant.State)
	assert.Equal(t, expectedTenant.Roles, actualTenant.Roles)
}