package runner

import (
	"encoding/json"
	"net/http"
	"strings"
)

type routeTokenHandler struct {
	managementKey   string
	baseURL         string
	capability      *routeCapability
	credentialAlive func(provider, authIndex string) bool
}

func newRouteTokenHandler(managementKey, baseURL string, capability *routeCapability, credentialAlive func(string, string) bool) http.Handler {
	return &routeTokenHandler{
		managementKey: managementKey, baseURL: strings.TrimRight(baseURL, "/"), capability: capability, credentialAlive: credentialAlive,
	}
}

func (h *routeTokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validControlAuthorization(r.Header.Get("Authorization"), h.managementKey) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPut && r.URL.Path == "/ao/internal/routes/bindings" {
		var input routeBindingSnapshot
		if decodeCredentialJSON(r, &input) != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if r.Context().Err() != nil || h.capability.Reconcile(input) != nil {
			writeRouteError(w, http.StatusConflict, "binding_changed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/ao/internal/routes/token" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var input struct {
		AccountID       string `json:"accountId"`
		BindingRevision int64  `json:"bindingRevision"`
		Provider        string `json:"provider"`
		AuthIndex       string `json:"authIndex"`
		SessionID       string `json:"sessionId"`
	}
	if err := decodeCredentialJSON(r, &input); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.AuthIndex = strings.TrimSpace(input.AuthIndex)
	input.SessionID = strings.TrimSpace(input.SessionID)
	claims := routeClaims{Provider: input.Provider, AuthIndex: input.AuthIndex, SessionID: input.SessionID, AccountID: input.AccountID, BindingRevision: input.BindingRevision}
	if !validRouteClaims(claims) {
		writeRouteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if input.AccountID != publicCredentialID(h.managementKey, input.AuthIndex) || !h.capability.admitsBinding(claims) {
		writeRouteError(w, http.StatusConflict, "binding_changed")
		return
	}
	if h.credentialAlive == nil || !h.credentialAlive(input.Provider, input.AuthIndex) {
		writeRouteError(w, http.StatusNotFound, "account_unavailable")
		return
	}
	token, err := h.capability.Mint(claims)
	if err != nil {
		writeRouteError(w, http.StatusConflict, "binding_changed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"baseUrl": h.baseURL, "token": token})
}

func writeRouteError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
