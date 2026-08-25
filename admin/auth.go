package admin

import "net/http"

// Principal represents the authenticated identity of a caller of the
// Admin API.
type Principal struct {
	ID    string
	Roles []string
}

// Authenticator verifies the identity of an incoming HTTP request to the
// Admin API. tenant-core does not provide any concrete implementation
// (JWT, API key, OIDC, mTLS...) — it is the responsibility of the
// integrating application to supply the mechanism that fits its
// infrastructure.
type Authenticator interface {
	Authenticate(r *http.Request) (*Principal, error)
}