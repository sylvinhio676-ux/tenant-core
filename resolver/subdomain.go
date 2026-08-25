package resolver

import (
	"errors"
	"net/http"
	"strings"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// ErrNoTenant is returned when no tenant can be identified.
var ErrNoTenant = errors.New("resolver: no tenant found in request")

// SubdomainResolver extracts the tenant from the request's subdomain.
// E.g. for baseDomain="myapp.com", "tenant-a.myapp.com" gives TenantID("tenant-a").
type SubdomainResolver struct {
	baseDomain string
}

// NewSubdomainResolver creates a SubdomainResolver for the given base domain.
func NewSubdomainResolver(baseDomain string) *SubdomainResolver {
	return &SubdomainResolver{baseDomain: baseDomain}
}

func (sr *SubdomainResolver) Resolve(r *http.Request) (tenant.TenantID, error) {
	host := r.Host

	// Strip an optional port (e.g. "tenant-a.myapp.com:8080")
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	suffix := "." + sr.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", ErrNoTenant
	}

	subdomain := strings.TrimSuffix(host, suffix)
	if subdomain == "" {
		return "", ErrNoTenant
	}

	if subdomain == "" || strings.Contains(subdomain, ".") {
    return "", ErrNoTenant
}

	return tenant.TenantID(subdomain), nil
}
