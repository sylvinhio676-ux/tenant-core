package resolver

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// Resolver identifie le tenant à partir d'une requête HTTP entrante.
type Resolver interface {
	Resolve(r *http.Request) (tenant.TenantID, error)
}