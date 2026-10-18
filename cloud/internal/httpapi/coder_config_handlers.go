package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
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
	// WorkerUser is "ao-worker" (default) or "workspace": run the worker and
	// agent as the workspace's own login user.
	WorkerUser string `json:"workerUser"`
	// ReadyProbe is an optional shell command that must exit 0, as the
	// workspace user, before AO launches its worker; polled every 5 seconds
	// for at most ReadyProbeTimeoutSeconds (default 900, 30..3600).
	ReadyProbe               string `json:"readyProbe"`
	ReadyProbeTimeoutSeconds int    `json:"readyProbeTimeoutSeconds"`
	// ReuseExistingCheckout adopts the template's existing clone of the
	// project repository; defaults to true with the workspace worker user and
	// is always false with ao-worker.
	ReuseExistingCheckout *bool `json:"reuseExistingCheckout"`
	// WorkingDirectory is where sessions work, for example ~/work/{repo}; it
	// requires the workspace worker user. Empty keeps AO's own checkout (or
	// the ReuseExistingCheckout search).
	WorkingDirectory string `json:"workingDirectory"`
	// ProtectSecretFiles keeps Claude Code from reading or editing .env and
	// .env.* files; defaults to true.
	ProtectSecretFiles *bool `json:"protectSecretFiles"`
}

// orgCoderConfigResponse is the secret-dropping view of an organization's Coder
// connection. It never carries the token — only the non-secret config plus the
// connection's validation metadata.
type orgCoderConfigResponse struct {
	Configured bool `json:"configured"`
	// Editable reports whether the organization may save a connection (it holds
	// the coder capability). One without it may still read and clear a stored
	// connection its sessions run on.
	Editable        bool                   `json:"editable"`
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
	if !s.requireOrgCoder(w, r, orgID) {
		return
	}
	// Every org that may run on Coder can read this. Desktop releases from before
	// managed Coder opened to every org load it for any org that runs on Coder
	// (Create Project, project settings, the Coder page), so an org without a
	// connection gets "none" rather than a refusal. An org that lost the coder
	// capability but still has a connection sees it, because its sessions still
	// run there; saving one stays capability-gated.
	editable := s.orgHasCustomCoder(principalFrom(r), orgID)
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
			Editable:        editable,
			CoderConfig:     &cfg,
			ValidationState: connection.ValidationState,
			ValidatedAt:     validatedAt,
			UpdatedAt:       &updatedAt,
		})
		return
	}
	writeJSON(w, http.StatusOK, orgCoderConfigResponse{Configured: false, Editable: editable})
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
	if !s.requireOrgCustomCoder(w, r, orgID) {
		return
	}
	// Only an admin may test a connection: a save can reuse the stored token and
	// records its outcome in the organization's activity trail.
	if store, ok := s.store.(orgAdminStore); ok {
		if err := store.RequireOrgAdmin(r.Context(), principalFrom(r), orgID); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	var body json.RawMessage
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	var request putOrgCoderConfigRequest
	var present map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&request)
	if decodeErr == nil {
		decodeErr = json.Unmarshal(body, &present)
	}
	clear(body)
	if decodeErr != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	token := []byte(strings.TrimSpace(request.Token))
	defer func() { clear(token) }() // also clears a reused stored token
	request.Token = ""
	// Every save is a connection test: trail its outcome so support can see
	// why a BYO Coder connection was rejected without asking the customer.
	validation := &coderValidation{result: "rejected", baseURL: request.BaseURL}
	defer s.recordCoderValidation(r, orgID, validation)
	if len(token) > 64<<10 {
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
		WorkerUser:                request.WorkerUser,
		ReadyProbe:                request.ReadyProbe,
		ReadyProbeTimeoutSeconds:  request.ReadyProbeTimeoutSeconds,
		ReuseExistingCheckout:     request.ReuseExistingCheckout,
		WorkingDirectory:          request.WorkingDirectory,
		ProtectSecretFiles:        request.ProtectSecretFiles,
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
	if stored, ok := s.storedCoderConfig(r, orgID); ok && stored.BaseURL == normalized.BaseURL {
		keepStoredCoderSettings(&normalized, stored, present)
	}
	// Editing an existing connection may omit the token: the stored one is kept
	// as long as the deployment (base URL) is unchanged. A first save, or a move
	// to another deployment, must supply a token.
	if len(token) == 0 {
		stored, ok := s.storedCoderToken(r, orgID, normalized.BaseURL)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "A Coder API token is required.")
			return
		}
		token = stored
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
		validation.reason, validation.err = "invalid_config", err
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
	if err := normalized.ValidateDevEnvironment(); err != nil {
		validation.reason, validation.err = "invalid_config", err
		code, message := "validation_error", err.Error()
		if errors.Is(err, domain.ErrCoderSettingRequiresWorkspaceUser) {
			code = "coder_setting_requires_workspace_user"
			message = "The working directory needs AO to run as the workspace user. Set the worker user to \"workspace\" first."
		}
		writeError(w, r, http.StatusUnprocessableEntity, code, message)
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
			validation.reason, validation.err = "coder_unreachable", resolveErr
			writeError(w, r, http.StatusBadGateway, "coder_unavailable", "Could not reach your Coder to resolve the workspace owner. Check the URL and API token.")
			return
		}
		normalized.Owner = owner
		validation.ownerResolved = true
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
	validation.result, validation.connectionID = "ok", connection.ID
	validatedAt := connection.ValidatedAt
	updatedAt := connection.UpdatedAt
	writeJSON(w, http.StatusOK, orgCoderConfigResponse{
		Configured: true,
		// Saving requires the coder capability (requireOrgCustomCoder above).
		Editable:        true,
		CoderConfig:     &normalized,
		ValidationState: connection.ValidationState,
		ValidatedAt:     validatedAt,
		UpdatedAt:       &updatedAt,
	})
}

// orgAdminStore checks that a principal administers an organization.
type orgAdminStore interface {
	RequireOrgAdmin(ctx context.Context, principal domain.Principal, orgID string) error
}

// storedCoderConfig returns the organization's stored Coder connection config.
func (s *Server) storedCoderConfig(r *http.Request, orgID string) (domain.OrgCoderConfig, bool) {
	store, ok := s.store.(coderConnectionForServiceStore)
	if !ok {
		return domain.OrgCoderConfig{}, false
	}
	_, _, config, _, err := store.CoderConnectionForService(r.Context(), orgID)
	if err != nil {
		return domain.OrgCoderConfig{}, false
	}
	stored, err := domain.DecodeOrgCoderConfig(config)
	if err != nil {
		return domain.OrgCoderConfig{}, false
	}
	return stored, true
}

// keepStoredCoderSettings carries the stored value of every setting the request
// leaves out, so a client that sends only some fields (an older desktop sends
// just the base URL and token) cannot reset the rest. The owner is not carried:
// a new token re-derives it.
func keepStoredCoderSettings(config *domain.OrgCoderConfig, stored domain.OrgCoderConfig, present map[string]json.RawMessage) {
	omitted := func(key string) bool { _, ok := present[key]; return !ok }
	if omitted("templateId") {
		config.TemplateID = stored.TemplateID
	}
	if omitted("agentName") {
		config.AgentName = stored.AgentName
	}
	if omitted("parameters") {
		config.Parameters = stored.Parameters
	}
	if omitted("durableRoot") {
		config.DurableRoot = stored.DurableRoot
	}
	if omitted("endpointServiceName") {
		config.EndpointServiceName = stored.EndpointServiceName
	}
	if omitted("region") {
		config.Region = stored.Region
	}
	if omitted("requireMountedDurableRoot") {
		config.RequireMountedDurableRoot = stored.RequireMountedDurableRoot
	}
	if omitted("startupTimeoutSeconds") {
		config.StartupTimeoutSeconds = stored.StartupTimeoutSeconds
	}
	if omitted("workerUser") {
		config.WorkerUser = stored.WorkerUser
	}
	if omitted("readyProbe") {
		config.ReadyProbe = stored.ReadyProbe
	}
	if omitted("readyProbeTimeoutSeconds") {
		config.ReadyProbeTimeoutSeconds = stored.ReadyProbeTimeoutSeconds
	}
	if omitted("reuseExistingCheckout") {
		config.ReuseExistingCheckout = stored.ReuseExistingCheckout
	}
	if omitted("workingDirectory") {
		config.WorkingDirectory = stored.WorkingDirectory
	}
	if omitted("protectSecretFiles") {
		config.ProtectSecretFiles = stored.ProtectSecretFiles
	}
}

// storedCoderToken returns the organization's stored Coder token when its
// existing connection points at baseURL, so a settings save can omit the
// token. It reports false when there is no connection, the deployment
// changed, or the stored token cannot be read. The caller owns (and clears)
// the returned bytes; the store write still enforces org-admin rights.
func (s *Server) storedCoderToken(r *http.Request, orgID, baseURL string) ([]byte, bool) {
	store, ok := s.store.(coderConnectionForServiceStore)
	if !ok {
		return nil, false
	}
	encrypted, nonce, config, _, err := store.CoderConnectionForService(r.Context(), orgID)
	if err != nil || len(encrypted) == 0 {
		return nil, false
	}
	existing, err := domain.DecodeOrgCoderConfig(config)
	if err != nil || existing.BaseURL != baseURL {
		return nil, false
	}
	token, err := s.secretCipher.Decrypt(encrypted, nonce, providerSecretAssociatedData(orgID, sandbox.ProviderCoder))
	if err != nil {
		s.logger.Warn("decrypt stored coder token", "error", err, "request_id", requestID(r))
		return nil, false
	}
	if len(bytes.TrimSpace(token)) == 0 {
		clear(token)
		return nil, false
	}
	return token, true
}

// deleteOrgCoderConfig removes an organization's bring-your-own Coder connection.
// It mirrors deleteAgentConnection: the delete is admin-gated inside the store
// (requireOrgAdmin). Clearing needs only that the org may run on Coder, not the
// coder capability, so an org that lost the capability can still remove a
// connection its sessions would otherwise keep using.
func (s *Server) deleteOrgCoderConfig(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if !s.requireOrgCoder(w, r, orgID) {
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
		if errors.Is(err, postgres.ErrProviderConnectionInUse) {
			writeError(w, r, http.StatusConflict, "connection_in_use",
				"Sessions still run on this Coder connection. Remove their projects or sessions first.")
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
