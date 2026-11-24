package controllers

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/access"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
)

// AccessController serves who the caller is, the action catalog, and the agent
// tokens of this machine. Token routes answer only on the loopback listener.
type AccessController struct {
	Registry *access.Registry
}

// Register mounts the access routes.
func (c *AccessController) Register(r chi.Router) {
	r.Get("/access/whoami", c.whoami)
	r.Get("/access/actions", c.listActions)
	r.Get("/access/actions/{actionId}", c.getAction)
	r.Get("/access/tokens", c.listTokens)
	r.Post("/access/tokens", c.createToken)
	r.Delete("/access/tokens/{tokenId}", c.revokeToken)
}

func grantResponse(grant access.Grant, now time.Time) AccessGrantResponse {
	out := AccessGrantResponse{Level: grant.Level, Projects: grant.Projects}
	if !grant.ExpiresAt.IsZero() {
		expiresAt := grant.ExpiresAt
		out.ExpiresAt, out.Ended = &expiresAt, !now.Before(expiresAt)
	}
	return out
}

func (c *AccessController) whoami(w http.ResponseWriter, r *http.Request) {
	who := access.CallerFrom(r.Context())
	out := WhoamiResponse{Kind: who.Kind, ID: who.ID, Name: who.Name}
	if who.Grant != nil {
		grant := grantResponse(*who.Grant, time.Now())
		out.Grant = &grant
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func actionResponse(action access.Action, who access.Caller) AccessActionResponse {
	out := AccessActionResponse{ID: action.ID, Level: action.Level, Method: action.Method, Path: action.Path, Allowed: true}
	if who.Grant != nil {
		refusal := who.Grant.Check(action, "", false)
		out.Allowed = refusal == nil || refusal.Reason == access.ReasonProject && action.Project != access.ProjectAll
	}
	if operation := apispec.Default().Operation(action.Method, action.Path); operation != nil {
		out.Summary, _ = operation["summary"].(string)
	}
	return out
}

func (c *AccessController) listActions(w http.ResponseWriter, r *http.Request) {
	who := access.CallerFrom(r.Context())
	catalog := access.Catalog()
	out := ListAccessActionsResponse{Actions: make([]AccessActionResponse, 0, len(catalog))}
	for _, action := range catalog {
		out.Actions = append(out.Actions, actionResponse(action, who))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *AccessController) getAction(w http.ResponseWriter, r *http.Request) {
	id, _ := url.PathUnescape(chi.URLParam(r, "actionId"))
	action, ok := access.ActionByID(id)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ACTION_NOT_FOUND", "The action catalog has no "+id, nil)
		return
	}
	detail := AccessActionDetailResponse{
		AccessActionResponse: actionResponse(action, access.CallerFrom(r.Context())),
		Parameters:           []map[string]any{},
	}
	spec := apispec.Default()
	if operation := spec.Operation(action.Method, action.Path); operation != nil {
		parameters, _ := spec.Inline(operation["parameters"]).([]any)
		for _, parameter := range parameters {
			if fields, ok := parameter.(map[string]any); ok && fields["in"] != "header" {
				detail.Parameters = append(detail.Parameters, fields)
			}
		}
		if body, ok := operation["requestBody"].(map[string]any); ok {
			content, _ := body["content"].(map[string]any)
			media, _ := content["application/json"].(map[string]any)
			detail.Body, _ = spec.Inline(media["schema"]).(map[string]any)
		}
	}
	envelope.WriteJSON(w, http.StatusOK, AccessActionEnvelope{Action: detail})
}

func tokenResponse(token access.Token, now time.Time) AccessTokenResponse {
	out := AccessTokenResponse{ID: token.ID, Name: token.Name, Grant: grantResponse(token.Grant, now), CreatedAt: token.CreatedAt}
	if !token.LastUsedAt.IsZero() {
		lastUsedAt := token.LastUsedAt
		out.LastUsedAt = &lastUsedAt
	}
	return out
}

func (c *AccessController) tokensUnavailable(w http.ResponseWriter, r *http.Request, method, path string) bool {
	if requestscope.IsLAN(r.Context()) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ROUTE_NOT_FOUND", "route not found", nil)
		return true
	}
	if c.Registry == nil {
		apispec.NotImplemented(w, r, method, path)
		return true
	}
	return false
}

func (c *AccessController) listTokens(w http.ResponseWriter, r *http.Request) {
	if c.tokensUnavailable(w, r, http.MethodGet, "/api/v1/access/tokens") {
		return
	}
	if err := c.Registry.Unreadable(); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal_error", "AGENT_TOKENS_UNREADABLE", err.Error(), nil)
		return
	}
	now := time.Now()
	tokens := c.Registry.List()
	out := ListAccessTokensResponse{Tokens: make([]AccessTokenResponse, 0, len(tokens))}
	for _, token := range tokens {
		out.Tokens = append(out.Tokens, tokenResponse(token, now))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *AccessController) createToken(w http.ResponseWriter, r *http.Request) {
	if c.tokensUnavailable(w, r, http.MethodPost, "/api/v1/access/tokens") {
		return
	}
	var req CreateAccessTokenRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid agent token request", nil)
		return
	}
	var expiresAt time.Time
	if req.ExpiresAt != nil {
		expiresAt = *req.ExpiresAt
	}
	now := time.Now()
	grant, err := access.NewGrant(req.Level, req.Projects, expiresAt)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "AGENT_TOKEN_INVALID_GRANT", err.Error(), nil)
		return
	}
	token, secret, err := c.Registry.Create(req.Name, grant, now)
	if errors.Is(err, access.ErrNotSaved) {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal_error", "AGENT_TOKEN_NOT_SAVED", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "AGENT_TOKEN_NOT_CREATED", err.Error(), nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusCreated, CreateAccessTokenResponse{Token: tokenResponse(token, now), Secret: secret})
}

func (c *AccessController) revokeToken(w http.ResponseWriter, r *http.Request) {
	if c.tokensUnavailable(w, r, http.MethodDelete, "/api/v1/access/tokens/{tokenId}") {
		return
	}
	id, _ := url.PathUnescape(chi.URLParam(r, "tokenId"))
	_, found, err := c.Registry.Revoke(id)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal_error", "AGENT_TOKEN_NOT_REVOKED", "The agent token could not be revoked", nil)
		return
	}
	if !found {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_TOKEN_NOT_FOUND", "No agent token with that id or name", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
