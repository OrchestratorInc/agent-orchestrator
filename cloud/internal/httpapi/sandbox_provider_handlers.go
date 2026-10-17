package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
)

// putOrgSandboxProvider stores the organization's default sandbox provider: the
// one sessions get when neither the request nor the project picks one. Only an
// organization allowed more than one provider has a choice to make, and only
// its admins may make it. An empty defaultProvider clears the choice. The
// response is the organization's offering with the new default.
func (s *Server) putOrgSandboxProvider(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	store, ok := s.store.(orgSandboxPreferenceStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Sandbox provider preferences are unavailable.")
		return
	}
	var input struct {
		DefaultProvider string `json:"defaultProvider"`
	}
	if err := decodeJSONLimit(w, r, &input, 4<<10); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	provider := strings.ToLower(strings.TrimSpace(input.DefaultProvider))
	principal := principalFrom(r)
	if store, ok := s.store.(orgMemberStore); ok {
		if err := store.RequireOrgMember(r.Context(), principal, orgID); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	allowed := s.providersForOrg(principal, orgID)
	if len(allowed) < 2 {
		writeError(w, r, http.StatusConflict, "no_provider_choice",
			"Your organization runs on a single sandbox provider, so there is no default to choose.")
		return
	}
	if provider != "" && !slices.Contains(allowed, provider) {
		writeProviderForbidden(w, r, provider, false)
		return
	}
	if err := store.SetOrgDefaultSandboxProvider(r.Context(), principal, orgID, provider); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	offering, err := s.offeringForOrg(r.Context(), principal, orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, offering)
}
