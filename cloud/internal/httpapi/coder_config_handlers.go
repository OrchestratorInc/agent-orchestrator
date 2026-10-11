package httpapi

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/coder"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// awsRegionPattern is a loose AWS-region shape (e.g. eu-north-1). It is only used
// to reject obviously wrong input for the optional PrivateLink Region field; blank
// is always allowed.
var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

// putOrgCoderConfigRequest is the body for setting an organization's
// bring-your-own Coder connection. The token is a secret, kept out of the config
// JSONB and stored only as the connection's encrypted_secret.
type putOrgCoderConfigRequest struct {
	Token       string            `json:"token"`
	BaseURL     string            `json:"baseUrl"`
	Owner       string            `json:"owner"`
	TemplateID  string            `json:"templateId"`
	AgentName   string            `json:"agentName"`
	Parameters  map[string]string `json:"parameters"`
	DurableRoot string            `json:"durableRoot"`
	// Optional PrivateLink coordinates for a Coder in a private VPC. Non-secret;
	// stored in the config JSONB and left blank for a directly reachable Coder.
	EndpointServiceName string `json:"endpointServiceName"`
	Region              string `json:"region"`
	// RequireMountedDurableRoot keeps the strict mounted-volume check for the
	// durable root; off by default for a bring-your-own template.
	RequireMountedDurableRoot bool `json:"requireMountedDurableRoot"`
	// StartupTimeoutSeconds bounds how long AO waits for a workspace to become
	// ready and its worker to start. Zero uses the 20-minute default.
	StartupTimeoutSeconds int `json:"startupTimeoutSeconds"`
}

// orgCoderConfigResponse is the secret-dropping view of an organization's Coder
// connection. It never carries the token — only the non-secret config plus the
// connection's validation metadata.
type orgCoderConfigResponse struct {
	Configured      bool                   `json:"configured"`
	CoderConfig     *domain.OrgCoderConfig `json:"coderConfig,omitempty"`
	ValidationState string                 `json:"validationState,omitempty"`
	ValidatedAt     *time.Time             `json:"validatedAt,omitempty"`
	UpdatedAt       *time.Time             `json:"updatedAt,omitempty"`
}

// getOrgCoderConfig returns an organization's bring-your-own Coder config. It
// reuses the list store (which never selects the encrypted secret), so the token
// cannot leak through this path.
func (s *Server) getOrgCoderConfig(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	store, ok := s.store.(providerConnectionStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Provider connections are unavailable.")
		return
	}
	connections, err := store.ListProviderConnections(r.Context(), principalFrom(r), orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	for _, connection := range connections {
		if connection.Provider != sandbox.ProviderCoder || connection.Label != defaultAgentConnectionLabel {
			continue
		}
		cfg, decodeErr := domain.DecodeOrgCoderConfig(connection.Config)
		if decodeErr != nil {
			s.logger.Error("decode organization coder config", "error", decodeErr, "request_id", requestID(r))
			writeError(w, r, http.StatusInternalServerError, "internal_error", "The organization's Coder configuration is invalid.")
			return
		}
		validatedAt := connection.ValidatedAt
		updatedAt := connection.UpdatedAt
		writeJSON(w, http.StatusOK, orgCoderConfigResponse{
			Configured:      true,
			CoderConfig:     &cfg,
			ValidationState: connection.ValidationState,
			ValidatedAt:     validatedAt,
			UpdatedAt:       &updatedAt,
		})
		return
	}
	writeJSON(w, http.StatusOK, orgCoderConfigResponse{Configured: false})
}

// putOrgCoderConfig stores an organization's bring-your-own Coder connection:
// the token is encrypted at rest, the non-secret fields live in the config
// JSONB. Writing requires the caller to be an org admin (enforced inside
// UpsertProviderConnection) and the organization to be entitled to the coder
// provider.
func (s *Server) putOrgCoderConfig(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if s.secretCipher == nil {
		writeError(w, r, http.StatusServiceUnavailable, "provider_connections_unavailable", "Provider credential storage is not configured.")
		return
	}
	if !s.orgAllowsProvider(principalFrom(r), sandbox.ProviderCoder) {
		writeError(w, r, http.StatusForbidden, "provider_forbidden", "Your organization is not enabled for the Coder sandbox provider.")
		return
	}
	var request putOrgCoderConfigRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	token := []byte(strings.TrimSpace(request.Token))
	defer clear(token)
	request.Token = ""
	if len(token) == 0 || len(token) > 64<<10 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "A Coder API token is required.")
		return
	}
	// Normalize once through the domain codec: it trims every field and fills the
	// durable-root default, so validation and storage see the same canonical form.
	configJSON, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL:                   request.BaseURL,
		Owner:                     request.Owner,
		TemplateID:                request.TemplateID,
		AgentName:                 request.AgentName,
		Parameters:                request.Parameters,
		DurableRoot:               request.DurableRoot,
		EndpointServiceName:       request.EndpointServiceName,
		Region:                    request.Region,
		RequireMountedDurableRoot: request.RequireMountedDurableRoot,
		StartupTimeoutSeconds:     request.StartupTimeoutSeconds,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The Coder configuration could not be stored.")
		return
	}
	normalized, err := domain.DecodeOrgCoderConfig(configJSON)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The Coder configuration could not be stored.")
		return
	}
	// The slimmed connection is just a base URL and a token. Validate that shape by
	// building a probe client: it checks the URL is an absolute http(s) origin — an
	// IP or host:port is allowed on purpose (bring-your-own Coder is often reached
	// privately), public HTTPS is NOT forced — and that the token is present. The
	// same client resolves the workspace owner below. Owner and template are no
	// longer pasted: owner is derived from the token, the template is chosen per
	// project, so neither is required here.
	probe, err := coder.New(coder.Config{BaseURL: normalized.BaseURL, Token: string(token)})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The Coder configuration is invalid: "+err.Error())
		return
	}
	// The durable root must still expand to a safe mount path (normalize fills the
	// default when the request omits it, which the slimmed form always does).
	if _, err := sandbox.NewCoderWorkspaceLayout(normalized.DurableRoot); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The Coder configuration is invalid: "+err.Error())
		return
	}
	if normalized.StartupTimeoutSeconds != 0 &&
		(normalized.StartupTimeoutSeconds < domain.MinOrgCoderStartupTimeoutSeconds ||
			normalized.StartupTimeoutSeconds > domain.MaxOrgCoderStartupTimeoutSeconds) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error",
			"The Coder startup timeout must be between 60 seconds and 2 hours.")
		return
	}
	// A template is optional at the org level — it is chosen per project now — but a
	// value that IS present (a legacy or explicit caller) must still be a UUID.
	if normalized.TemplateID != "" {
		if _, err := uuid.Parse(normalized.TemplateID); err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The Coder template ID must be a UUID.")
			return
		}
	}
	// Derive the workspace owner from the token when the caller did not supply one
	// (the slimmed form never does). Resolving and storing it here keeps every
	// downstream path — session creation, the resolver, the reconciler — working
	// against a concrete owner with no further change, and validates the token and
	// Coder reachability as a side effect. An explicit owner is left untouched for
	// backward compatibility with configs that already carry one.
	if normalized.Owner == "" {
		owner, resolveErr := probe.CurrentUser(r.Context())
		if resolveErr != nil {
			s.logger.Error("resolve coder owner from token", "error", resolveErr, "request_id", requestID(r))
			writeError(w, r, http.StatusBadGateway, "coder_unavailable", "Could not reach your Coder to resolve the workspace owner. Check the URL and API token.")
			return
		}
		normalized.Owner = owner
	}
	// The PrivateLink fields are optional (blank for a directly reachable Coder).
	// When present, apply only a loose shape check — AO ops provisions the endpoint
	// off them, so the values are not consumed here and must not be over-constrained.
	if normalized.EndpointServiceName != "" &&
		(!strings.HasPrefix(normalized.EndpointServiceName, "com.amazonaws.vpce.") ||
			!strings.Contains(normalized.EndpointServiceName, "vpce-svc-")) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The VPC endpoint service name must look like com.amazonaws.vpce.<region>.vpce-svc-….")
		return
	}
	if normalized.Region != "" && !awsRegionPattern.MatchString(normalized.Region) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The AWS region must look like eu-north-1.")
		return
	}
	// Re-encode with the resolved owner so the stored config and the response agree.
	configJSON, err = domain.EncodeOrgCoderConfig(normalized)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The Coder configuration could not be stored.")
		return
	}
	encrypted, nonce, err := s.secretCipher.Encrypt(token, providerSecretAssociatedData(orgID, sandbox.ProviderCoder))
	if err != nil {
		s.logger.Error("encrypt coder token", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The Coder token could not be stored.")
		return
	}
	store, ok := s.store.(providerConnectionStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Provider connections are unavailable.")
		return
	}
	connection, err := store.UpsertProviderConnection(
		r.Context(),
		principalFrom(r),
		orgID,
		sandbox.ProviderCoder,
		defaultAgentConnectionLabel,
		encrypted,
		nonce,
		configJSON,
	)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	validatedAt := connection.ValidatedAt
	updatedAt := connection.UpdatedAt
	writeJSON(w, http.StatusOK, orgCoderConfigResponse{
		Configured:      true,
		CoderConfig:     &normalized,
		ValidationState: connection.ValidationState,
		ValidatedAt:     validatedAt,
		UpdatedAt:       &updatedAt,
	})
}

// deleteOrgCoderConfig removes an organization's bring-your-own Coder connection.
// It mirrors deleteAgentConnection: the delete is admin-gated inside the store
// (requireOrgAdmin), and capability-gated here so an org that is not entitled to
// the coder provider can neither write nor clear a connection.
func (s *Server) deleteOrgCoderConfig(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if !s.orgAllowsProvider(principalFrom(r), sandbox.ProviderCoder) {
		writeError(w, r, http.StatusForbidden, "provider_forbidden", "Your organization is not enabled for the Coder sandbox provider.")
		return
	}
	store, ok := s.store.(providerConnectionStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Provider connections are unavailable.")
		return
	}
	if err := store.DeleteProviderConnection(
		r.Context(),
		principalFrom(r),
		orgID,
		sandbox.ProviderCoder,
		defaultAgentConnectionLabel,
	); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
