package resolver

import (
	"errors"
	"net/http"
	"strings"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// ErrNoTenant est retournée quand aucun tenant ne peut être identifié.
var ErrNoTenant = errors.New("resolver: no tenant found in request")

// SubdomainResolver extrait le tenant depuis le sous-domaine de la requête.
// Ex: pour baseDomain="myapp.com", "tenant-a.myapp.com" donne TenantID("tenant-a").
type SubdomainResolver struct {
	baseDomain string
}

// NewSubdomainResolver crée un SubdomainResolver pour le domaine de base donné.
func NewSubdomainResolver(baseDomain string) *SubdomainResolver {
	return &SubdomainResolver{baseDomain: baseDomain}
}

func (sr *SubdomainResolver) Resolve(r *http.Request) (tenant.TenantID, error) {
	host := r.Host

	// On retire un éventuel port (ex: "tenant-a.myapp.com:8080")
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