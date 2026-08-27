package rbac

import (
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// FuzzRBAC_CanNeverGrantsUndefinedAccess fuzzes RBAC.Can against a fixed,
// known set of role/permission definitions, checking the one invariant that
// must hold for every possible (tenantID, role, permission) input: Can must
// never report a permission as granted unless that exact permission was
// explicitly attached, via DefineRole, to a role the tenant actually holds,
// for that exact tenant.
//
// The RBAC instance and its definitions are built once, in the test body,
// before fuzzing starts — not derived from the corpus — so every fuzzed
// input is checked against one known-correct oracle rather than a moving
// target.
//
// `go test ./rbac/...` (or `go test -race ./...` at the repo root) only
// replays the seed corpus below once and does not mutate it — that's enough
// to catch a regression the seeds already describe, but it is not fuzzing.
// To actually fuzz — have the Go runtime generate and try new input
// combinations beyond the seed corpus — run explicitly, e.g.:
//
//	go test -fuzz=FuzzRBAC_CanNeverGrantsUndefinedAccess -fuzztime=30s ./rbac/...
//
// A failing case is written to rbac/testdata/fuzz/<FuzzName>/ and is
// automatically replayed by every future `go test` run from then on.
func FuzzRBAC_CanNeverGrantsUndefinedAccess(f *testing.F) {
	r := New()
	r.DefineRole("tenant-A", "admin", "users:read", "users:write")
	r.DefineRole("tenant-A", "viewer", "users:read")
	r.DefineRole("tenant-B", "admin", "billing:write")

	// granted records exactly what the RBAC instance above was told to
	// grant, so the fuzz target has a ground truth to check Can's answer
	// against, independent of RBAC's own internal map.
	type grant struct {
		tenantID   tenant.TenantID
		role       tenant.Role
		permission Permission
	}
	granted := []grant{
		{"tenant-A", "admin", "users:read"},
		{"tenant-A", "admin", "users:write"},
		{"tenant-A", "viewer", "users:read"},
		{"tenant-B", "admin", "billing:write"},
	}

	isGranted := func(tenantID tenant.TenantID, role tenant.Role, permission Permission) bool {
		for _, g := range granted {
			if g.tenantID == tenantID && g.role == role && g.permission == permission {
				return true
			}
		}
		return false
	}

	// Seed corpus: a mix of known-granted, known-not-granted, and
	// nonsense/edge-case values (empty strings, values that look like
	// definitions for a different tenant/role/permission).
	f.Add("tenant-A", "admin", "users:read")
	f.Add("tenant-A", "admin", "users:delete")
	f.Add("tenant-A", "viewer", "users:write")
	f.Add("tenant-B", "admin", "billing:write")
	f.Add("tenant-B", "admin", "users:read")
	f.Add("tenant-C", "admin", "users:read")
	f.Add("", "", "")
	f.Add("tenant-A", "", "users:read")
	f.Add("tenant-A", "admin", "")

	f.Fuzz(func(t *testing.T, tenantIDStr, roleStr, permissionStr string) {
		tenantID := tenant.TenantID(tenantIDStr)
		role := tenant.Role(roleStr)
		permission := Permission(permissionStr)

		tt := &tenant.Tenant{ID: tenantID, Roles: []tenant.Role{role}}
		got := r.Can(tt, permission)

		want := isGranted(tenantID, role, permission)
		if got && !want {
			t.Fatalf(
				"Can granted an access that was never defined: tenantID=%q role=%q permission=%q",
				tenantIDStr, roleStr, permissionStr,
			)
		}
	})
}
