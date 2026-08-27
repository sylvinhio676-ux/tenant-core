package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// HTTPHandler exposes Service via a plain net/http REST API.
type HTTPHandler struct {
	mux           *http.ServeMux
	service       *Service
	authenticator Authenticator
}

// HTTPHandlerOption configures a HTTPHandler at creation time.
type HTTPHandlerOption func(*HTTPHandler)

// WithAuthenticator protects the Admin API by requiring every request to
// be authenticated before reaching an administrative operation.
//
// WARNING: without this option, the Admin API accepts requests without
// any authentication — this mode is only suitable for local development
// or tests, never for a deployment exposed to untrusted callers.
func WithAuthenticator(a Authenticator) HTTPHandlerOption {
	return func(h *HTTPHandler) {
		h.authenticator = a
	}
}

// NewHTTPHandler creates a ready-to-use HTTPHandler. Without
// WithAuthenticator, the Admin API does not authenticate any request —
// see WithAuthenticator to secure this endpoint.
func NewHTTPHandler(service *Service, opts ...HTTPHandlerOption) *HTTPHandler {
	h := &HTTPHandler{
		mux:     http.NewServeMux(),
		service: service,
	}

	for _, opt := range opts {
		opt(h)
	}

	h.mux.HandleFunc("PATCH /tenants/{id}/ban", h.withAuth(h.handleBan))
	h.mux.HandleFunc("PATCH /tenants/{id}/disable", h.withAuth(h.handleDisable))
	h.mux.HandleFunc("PATCH /tenants/{id}/activate", h.withAuth(h.handleActivate))

	return h
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// withAuth wraps an admin handler with the authentication check, if an
// Authenticator has been configured.
func (h *HTTPHandler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.authenticator == nil {
			next(w, r)
			return
		}

		_, err := h.authenticator.Authenticate(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		next(w, r)
	}
}

func (h *HTTPHandler) handleBan(w http.ResponseWriter, r *http.Request) {
	id := tenant.TenantID(r.PathValue("id"))
	if err := h.service.Ban(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleDisable(w http.ResponseWriter, r *http.Request) {
	id := tenant.TenantID(r.PathValue("id"))
	if err := h.service.Disable(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleActivate(w http.ResponseWriter, r *http.Request) {
	id := tenant.TenantID(r.PathValue("id"))
	if err := h.service.Activate(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeError maps err to the appropriate HTTP status and writes it as a
// JSON body. Known sentinel errors from the Store/AdminStore contract get
// their specific status; any other error falls back to 500, since its
// cause cannot be classified here.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, tenant.ErrTenantNotFound):
		status = http.StatusNotFound
	case errors.Is(err, tenant.ErrTenantAlreadyExists):
		status = http.StatusConflict
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
