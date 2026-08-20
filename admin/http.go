package admin

import (
	"encoding/json"
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

/**
 * HTTPHandler expose Service via une API REST en net/http pur, sans
	dépendance à un framework — voir cahier des charges section 8
	(agnosticisme). Peut être montée sur n'importe quel serveur HTTP Go.
 */
type HTTPHandler struct {
	mux     *http.ServeMux
	service *Service
}

// NewHTTPHandler crée un HTTPHandler prêt à l'emploi, avec ses routes déjà
// enregistrées.
func NewHTTPHandler(service *Service) *HTTPHandler {
	h := &HTTPHandler{
		mux:     http.NewServeMux(),
		service: service,
	}

	h.mux.HandleFunc("PATCH /tenants/{id}/ban", h.handleBan)
	h.mux.HandleFunc("PATCH /tenants/{id}/disable", h.handleDisable)
	h.mux.HandleFunc("PATCH /tenants/{id}/activate", h.handleActivate)

	return h
}

// ServeHTTP fait de HTTPHandler un http.Handler standard, intégrable
// directement dans n'importe quel serveur Go.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
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

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}