package resolver

import (
	"net/http/httptest"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestSubdomainResolver_Resolve(t *testing.T) {
	resolver := NewSubdomainResolver("myapp.com")

	// 1. Sous-domaine valide
	req := httptest.NewRequest(
		"GET",
		"https://tenant-a.myapp.com/users",
		nil,
	)

	got, err := resolver.Resolve(req)

	assert.NoError(t, err)
	assert.Equal(t, tenant.TenantID("tenant-a"), got)

	// 2. Host qui ne correspond pas au domaine de base
	req = httptest.NewRequest(
		"GET",
		"https://example.com/users",
		nil,
	)

	_, err = resolver.Resolve(req)

	assert.ErrorIs(t, err, ErrNoTenant)

	// 3. Plusieurs niveaux de sous-domaine
	req = httptest.NewRequest(
		"GET",
		"https://a.b.myapp.com/users",
		nil,
	)

	_, err = resolver.Resolve(req)

	assert.ErrorIs(t, err, ErrNoTenant)
}