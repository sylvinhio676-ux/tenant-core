package cachekey

import (
	"fmt"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// Key construit une clé de cache isolée par tenant.
func Key(tenantID tenant.TenantID, key string) string {
	return fmt.Sprintf("tenant:%s:%s", tenantID, key)
}