package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	ginlib "github.com/gin-gonic/gin"
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

func (f *fakeStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	return f.tenant, f.err
}

func (f *fakeStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

func TestMiddleware_InjectsTenantIntoContext(t *testing.T) {
	expectedTenant := &tenant.Tenant{
		ID:    "tenant-A",
		State: tenant.Active,
		Roles: []string{"admin"},
	}

	manager := tenant.New(
		tenant.WithResolver(&fakeResolver{id: "tenant-A"}),
		tenant.WithStore(&fakeStore{tenant: expectedTenant}),
	)

	recorder := httptest.NewRecorder()
	c, _ := ginlib.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/", nil)

	middleware := Middleware(manager)
	middleware(c)

	// Le middleware doit avoir remplacé le contexte de la requête
	// par celui contenant le tenant.
	actualTenant := tenantctx.FromContext(c.Request.Context())
	require.NotNil(t, actualTenant)
	assert.Equal(t, expectedTenant.ID, actualTenant.ID)
	assert.Equal(t, expectedTenant.State, actualTenant.State)
	assert.Equal(t, expectedTenant.Roles, actualTenant.Roles)
}