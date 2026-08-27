# Changelog

All notable changes to this project are documented in this file, one section
per version, newest first. This file starts tracking from the change below —
earlier releases (`v0.1.0`, `v0.2.0`) predate it and are not backfilled.

## Unreleased (targeting v0.3.0)

### Breaking

- **`rbac`: permissions are now typed as `rbac.Permission` instead of a plain `string`.**
  - `rbac.Permission` is a named `string` type (`type Permission string`), for the same type-safety reason as `tenant.TenantID`: it stops a permission from being mixed up with an unrelated string at compile time.
  - `RBAC.DefineRole(tenantID tenant.TenantID, role tenant.Role, permissions ...Permission)` — the `permissions` parameter is now variadic (was `[]string`). Existing calls passing a slice literal must switch to passing the permissions directly:
    ```go
    // before
    authz.DefineRole("acme", "admin", []string{"users:read", "users:write"})
    // after
    authz.DefineRole("acme", "admin", "users:read", "users:write")
    ```
  - `RBAC.Can(t *tenant.Tenant, permission Permission) bool` — the `permission` parameter is now `Permission` instead of `string`.
  - **Migration impact**: a string *literal* (e.g. `"users:write"`) keeps working unchanged at every call site, thanks to Go's implicit conversion of untyped string constants to a named string type. Only code passing a `string` **variable**, or a `[]string` **slice**, to `DefineRole` or `Can` needs an explicit conversion (`rbac.Permission(myVar)`, or unpack the slice into variadic arguments).
  - This is a breaking change and is not backwards compatible — it targets `v0.3.0`, not a patch release.

- **`tenant`: roles are now typed as `tenant.Role` instead of a plain `string`.**
  - `tenant.Role` is a named `string` type (`type Role string`), for the same type-safety reason as `tenant.TenantID` and `rbac.Permission`. It lives in the root `tenant` package rather than in `rbac` — even though `rbac` is its main consumer — because `Tenant.Roles` needs it, and the established dependency direction is `tenant → rbac`, never the reverse (putting `Role` in `rbac` would force `tenant.go` to import `rbac`, creating a cycle).
  - `Tenant.Roles` is now `[]Role` (was `[]string`).
  - `RBAC.DefineRole`'s `role` parameter is now `tenant.Role` (was `string`) — see the signature above.
  - **Migration impact**: a string literal (e.g. `"admin"`) keeps working unchanged wherever a `Role` is expected, same implicit-conversion reasoning as `Permission`. Existing `Tenant{..., Roles: []string{"admin"}}` literals need to become `Tenant{..., Roles: []tenant.Role{"admin"}}`:
    ```go
    // before
    &tenant.Tenant{ID: "acme", State: tenant.Active, Roles: []string{"admin"}}
    // after
    &tenant.Tenant{ID: "acme", State: tenant.Active, Roles: []tenant.Role{"admin"}}
    ```
  - This is a breaking change and is not backwards compatible — it targets `v0.3.0`, not a patch release.
