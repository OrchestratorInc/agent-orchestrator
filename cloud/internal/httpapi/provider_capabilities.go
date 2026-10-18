package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// orgCapabilities returns the effective entitlement flags for one organization.
// The token's WorkOS organization metadata (principal.OrgCapabilities) describes
// only the organization the token's WorkOS organization maps to
// (principal.TeamOrgID); it never applies to the user's personal organization or
// any other one. Any organization without its own capabilities, including one
// whose WorkOS metadata has no capabilities key, gets the deployment defaults
// (AO_CLOUD_DEFAULT_ORG_CAPABILITIES).
func (s *Server) orgCapabilities(principal domain.Principal, orgID string) []string {
	if orgID != "" && orgID == principal.TeamOrgID && len(principal.OrgCapabilities) > 0 {
		return principal.OrgCapabilities
	}
	return s.defaultOrgCapabilities
}

// orgAllowsProvider reports whether an organization may use the given sandbox
// provider. Coder and Freestyle are open to every organization: each picks its
// default in the desktop's settings (putOrgSandboxProvider), and plans meter
// both (sandboxBilledTx). An organization with the coder capability starts on
// Coder and runs it free, and may still buy Freestyle on a plan. Any other
// provider keeps the capability gate: when it is listed in AO_CLOUD_CAPABILITY_GATED_PROVIDERS, the
// organization must hold a capability of the same name (see orgCapabilities).
// It does not check that the deployment offers the provider; providersForOrg
// does both.
func (s *Server) orgAllowsProvider(principal domain.Principal, orgID, provider string) bool {
	if provider == sandbox.ProviderCoder || provider == sandbox.ProviderFreestyle {
		return true
	}
	if !s.capabilityGatedProviders[provider] {
		return true
	}
	return slices.Contains(s.orgCapabilities(principal, orgID), provider)
}

// orgHasCoderCapability reports whether an organization holds the coder
// capability: WorkOS organization metadata capabilities=coder, or the
// deployment defaults (AO_CLOUD_DEFAULT_ORG_CAPABILITIES). Such an organization
// starts on Coder and runs it free of any plan. It reads the capability alone,
// never the capability gate, so emptying AO_CLOUD_CAPABILITY_GATED_PROVIDERS
// cannot make Coder free for everyone.
func (s *Server) orgHasCoderCapability(principal domain.Principal, orgID string) bool {
	return slices.Contains(s.orgCapabilities(principal, orgID), sandbox.ProviderCoder)
}

// orgHasCustomCoder reports whether the organization may connect its own Coder
// deployment: this deployment offers Coder, and the organization holds the coder
// capability or the deployment does not gate coder (a self-hosted control plane
// without WorkOS capabilities keeps the page, as before).
func (s *Server) orgHasCustomCoder(principal domain.Principal, orgID string) bool {
	return slices.Contains(s.availableSandboxProviders, sandbox.ProviderCoder) &&
		(!s.capabilityGatedProviders[sandbox.ProviderCoder] || s.orgHasCoderCapability(principal, orgID))
}

// providersForOrg is the organization's effective provider list: what this
// deployment offers, filtered by the capability gate. The client never sees a
// provider the server would refuse on create.
func (s *Server) providersForOrg(principal domain.Principal, orgID string) []string {
	allowed := make([]string, 0, len(s.availableSandboxProviders))
	for _, provider := range s.availableSandboxProviders {
		if s.orgAllowsProvider(principal, orgID, provider) {
			allowed = append(allowed, provider)
		}
	}
	return allowed
}

// orgProviderAvailable reports whether the provider is both offered by this
// deployment and allowed for the organization.
func (s *Server) orgProviderAvailable(principal domain.Principal, orgID, provider string) bool {
	return slices.Contains(s.availableSandboxProviders, provider) &&
		s.orgAllowsProvider(principal, orgID, provider)
}

// orgSandboxPreferenceStore reads and stores the provider an organization chose
// in settings. The Postgres store implements it; a store without it leaves
// every organization on its default.
type orgSandboxPreferenceStore interface {
	OrgDefaultSandboxProvider(ctx context.Context, orgID string) (string, error)
	SetOrgDefaultSandboxProvider(ctx context.Context, principal domain.Principal, orgID, provider string) error
}

// defaultProviderForOrg is the provider a new project, or a session nothing
// else places, gets. With more than one provider, the organization's choice in
// settings comes first; otherwise baseProviderForOrg.
func (s *Server) defaultProviderForOrg(ctx context.Context, principal domain.Principal, orgID string) string {
	allowed := s.providersForOrg(principal, orgID)
	if len(allowed) > 1 {
		if chosen := s.orgChosenProvider(ctx, orgID); chosen != "" && slices.Contains(allowed, chosen) {
			return chosen
		}
	}
	return s.baseProviderForOrg(principal, orgID)
}

// baseProviderForOrg is the organization's provider before any choice in
// settings: Coder for an organization with the coder capability, Freestyle for
// everyone else (every new organization), then the deployment default when
// offered, then the first provider, then "" (no cloud sessions).
func (s *Server) baseProviderForOrg(principal domain.Principal, orgID string) string {
	allowed := s.providersForOrg(principal, orgID)
	if s.orgHasCoderCapability(principal, orgID) && slices.Contains(allowed, sandbox.ProviderCoder) {
		return sandbox.ProviderCoder
	}
	if slices.Contains(allowed, sandbox.ProviderFreestyle) {
		return sandbox.ProviderFreestyle
	}
	if slices.Contains(allowed, s.sandboxProvider) {
		return s.sandboxProvider
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

// projectProvider is the provider a project's sessions run on: the project's
// sandboxProvider, else Coder for a project carrying Coder dev-kit config (set
// up on Coder before projects recorded their provider), else "" (the
// organization's default applies).
func projectProvider(config json.RawMessage) string {
	if provider := domain.DecodeProjectSandboxProvider(config); provider != "" {
		return provider
	}
	if _, ok := domain.DecodeProjectCoderConfig(config); ok {
		return sandbox.ProviderCoder
	}
	return ""
}

// pinProjectProvider records on a new project's config the provider its
// sessions will run on, when the organization has more than one: the
// project's own (projectProvider), else the organization's default. Changing
// the default in settings later then moves no existing project. With a single
// provider the config is left as is, so the project follows the deployment.
func (s *Server) pinProjectProvider(
	ctx context.Context, principal domain.Principal, orgID string, config json.RawMessage,
) (json.RawMessage, error) {
	if domain.DecodeProjectSandboxProvider(config) != "" || len(s.providersForOrg(principal, orgID)) < 2 {
		return config, nil
	}
	provider := projectProvider(config)
	if provider == "" {
		provider = s.defaultProviderForOrg(ctx, principal, orgID)
	}
	if provider == "" {
		return config, nil
	}
	fields := map[string]json.RawMessage{}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &fields); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(provider)
	if err != nil {
		return nil, err
	}
	fields["sandboxProvider"] = encoded
	return json.Marshal(fields)
}

// orgChosenProvider is the provider the organization chose in settings, or ""
// when it has not chosen one or it cannot be read; a read failure falls back to
// the organization's default rather than failing session creation.
func (s *Server) orgChosenProvider(ctx context.Context, orgID string) string {
	store, ok := s.store.(orgSandboxPreferenceStore)
	if !ok || orgID == "" {
		return ""
	}
	provider, err := store.OrgDefaultSandboxProvider(ctx, orgID)
	if err != nil {
		if !errors.Is(err, postgres.ErrNotFound) {
			s.logger.Warn("read organization default sandbox provider", "org_id", orgID, "error", err)
		}
		return ""
	}
	return provider
}

// billingModeForOrg computes domain.BillingMode for one organization: plans
// apply to every organization offered a billed provider (Freestyle, or Coder
// without the coder capability), contract and internal ones included.
func (s *Server) billingModeForOrg(_ context.Context, principal domain.Principal, orgID string) (string, error) {
	if !s.billingReady() || !s.orgOffersBilledProvider(principal, orgID) {
		// An organization offered nothing billed has nothing to buy, matching
		// GET /billing.
		return domain.BillingModeOff, nil
	}
	return domain.BillingMode(true), nil
}

// offeringResponse is the /me "offering" object: which sandbox providers the
// organization may use and how it is billed. The server is the source of truth
// for both; the desktop shapes its UI from it.
type offeringResponse struct {
	Providers       []string `json:"providers"`
	DefaultProvider string   `json:"defaultProvider"`
	BillingMode     string   `json:"billingMode"`
	Kind            string   `json:"kind"`
	Features        []string `json:"features"`
	// CustomCoder: the organization may connect its own Coder deployment (it
	// holds the coder capability), or still has a connection its sessions run
	// on; the desktop shows the Coder settings page.
	CustomCoder bool `json:"customCoder"`
}

func (s *Server) offeringForOrg(ctx context.Context, principal domain.Principal, orgID string) (offeringResponse, error) {
	providers := s.providersForOrg(principal, orgID)
	mode, err := s.billingModeForOrg(ctx, principal, orgID)
	if err != nil {
		return offeringResponse{}, err
	}
	return offeringResponse{
		Providers:       providers,
		DefaultProvider: s.defaultProviderForOrg(ctx, principal, orgID),
		BillingMode:     mode,
		Kind:            domain.OfferingKind(providers),
		// Plan feature flags arrive with the paywall; none are gated yet.
		Features: []string{},
		CustomCoder: s.orgHasCustomCoder(principal, orgID) ||
			(slices.Contains(providers, sandbox.ProviderCoder) && s.orgHasCoderConnection(ctx, principal, orgID)),
	}, nil
}

// orgHasCoderConnection reports whether the organization has a stored
// bring-your-own Coder connection. Its Coder sessions run there even after it
// loses the coder capability, so the settings page stays reachable to inspect
// or clear it. A failed read counts as none.
func (s *Server) orgHasCoderConnection(ctx context.Context, principal domain.Principal, orgID string) bool {
	store, ok := s.store.(providerConnectionStore)
	if !ok || orgID == "" {
		return false
	}
	connections, err := store.ListProviderConnections(ctx, principal, orgID)
	if err != nil {
		return false
	}
	for _, connection := range connections {
		if connection.Provider == sandbox.ProviderCoder && connection.Label == defaultAgentConnectionLabel {
			return true
		}
	}
	return false
}

// orgMemberStore checks that a principal is an active member of an
// organization.
type orgMemberStore interface {
	RequireOrgMember(ctx context.Context, principal domain.Principal, orgID string) error
}

// requireOrgCoder writes 403 and returns false unless the caller is a member of
// the organization and the organization's effective providers include coder.
// Every Coder-only endpoint (bring-your-own connection config, template listing)
// calls it first. Membership is checked here because some of those endpoints
// read the organization's stored Coder token as the service, not as the caller.
func (s *Server) requireOrgCoder(w http.ResponseWriter, r *http.Request, orgID string) bool {
	principal := principalFrom(r)
	if store, ok := s.store.(orgMemberStore); ok {
		if err := store.RequireOrgMember(r.Context(), principal, orgID); err != nil {
			s.writeStoreError(w, r, err)
			return false
		}
	}
	if s.orgProviderAvailable(principal, orgID, sandbox.ProviderCoder) {
		return true
	}
	writeError(w, r, http.StatusForbidden, "provider_forbidden", "Your organization is not enabled for the Coder sandbox provider.")
	return false
}

// requireOrgCustomCoder is requireOrgCoder for the bring-your-own Coder
// connection endpoints: the organization must also hold the coder capability.
// The shared deployment's templates stay open to every organization that may
// run on Coder (requireOrgCoder).
func (s *Server) requireOrgCustomCoder(w http.ResponseWriter, r *http.Request, orgID string) bool {
	if !s.requireOrgCoder(w, r, orgID) {
		return false
	}
	if s.orgHasCustomCoder(principalFrom(r), orgID) {
		return true
	}
	writeError(w, r, http.StatusForbidden, "provider_forbidden",
		"Your organization is not enabled to connect its own Coder deployment.")
	return false
}

// writeProviderForbidden reports a provider the organization may not use.
// fromProject distinguishes a provider saved on the project from one the client
// sent, so the message says what to change.
func writeProviderForbidden(w http.ResponseWriter, r *http.Request, provider string, fromProject bool) {
	if provider == "" {
		writeError(
			w, r, http.StatusForbidden, "provider_forbidden",
			"Your organization is not enabled for any sandbox provider on this control plane.",
		)
		return
	}
	if fromProject {
		writeError(
			w, r, http.StatusForbidden, "provider_forbidden",
			"This project is set to run on the "+provider+" sandbox provider, which your organization is no longer enabled for. Create a new project to continue.",
		)
		return
	}
	writeError(
		w, r, http.StatusForbidden, "provider_forbidden",
		"Your organization is not enabled for the selected sandbox provider.",
	)
}

// validateProjectSandboxProvider rejects a project config whose saved
// sandboxProvider the organization cannot use. It writes 422
// provider_unavailable and returns false on rejection.
func (s *Server) validateProjectSandboxProvider(
	w http.ResponseWriter,
	r *http.Request,
	principal domain.Principal,
	orgID string,
	config json.RawMessage,
) bool {
	provider := domain.DecodeProjectSandboxProvider(config)
	if provider == "" || s.orgProviderAvailable(principal, orgID, provider) {
		return true
	}
	writeError(
		w, r, http.StatusUnprocessableEntity, "provider_unavailable",
		"The project's sandbox provider is not available to your organization.",
	)
	return false
}

// errInvalidOrgCoderConfig marks an organization Coder connection whose stored
// config does not decode.
var errInvalidOrgCoderConfig = errors.New("invalid organization coder config")

// writeCoderPlacementError reports a failure from resolveCoderPlacement or
// orgCoderPlacement.
func (s *Server) writeCoderPlacementError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errInvalidOrgCoderConfig) {
		s.logger.Error("decode organization coder config", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The organization's Coder configuration is invalid.")
		return
	}
	s.writeStoreError(w, r, err)
}

// coderPlacement is where a Coder session runs: the project's dev-kit options,
// and the organization's bring-your-own deployment when it has one.
type coderPlacement struct {
	options  *sandbox.CoderSessionOptions
	override *sandbox.CoderDeploymentOverride
	// connectionID is the bring-your-own provider connection the session binds
	// to, or "" for the deployment-default Coder.
	connectionID string
}

// resolveCoderPlacement builds the Coder placement for a session from its
// project config and the organization's provider connections. The project's
// Coder config (template, size, startup script, workspace prefix) becomes the
// session options. A bring-your-own Coder organization points its sessions at
// its own Coder deployment: preferredConnectionID (a child inheriting its
// parent's connection) is used when it names one of the organization's Coder
// connections, otherwise the organization's default Coder connection. With no
// connection the deployment default is kept, so deployment-level Coder is
// untouched.
func resolveCoderPlacement(
	projectConfig json.RawMessage,
	connections []domain.ProviderConnection,
	preferredConnectionID string,
) (coderPlacement, error) {
	var placement coderPlacement
	if cfg, ok := domain.DecodeProjectCoderConfig(projectConfig); ok {
		placement.options = &sandbox.CoderSessionOptions{
			TemplateID:          cfg.TemplateID,
			Size:                cfg.Size,
			StartupScript:       cfg.StartupScript,
			WorkspaceNamePrefix: cfg.WorkspaceNamePrefix,
			Parameters:          cfg.Parameters,
		}
	}
	var chosen *domain.ProviderConnection
	for i := range connections {
		connection := &connections[i]
		if connection.Provider != sandbox.ProviderCoder {
			continue
		}
		if preferredConnectionID != "" && connection.ID == preferredConnectionID {
			chosen = connection
			break
		}
		if chosen == nil && connection.Label == defaultAgentConnectionLabel {
			chosen = connection
		}
	}
	if chosen == nil {
		return placement, nil
	}
	cfg, err := domain.DecodeOrgCoderConfig(chosen.Config)
	if err != nil {
		return coderPlacement{}, fmt.Errorf("%w: %v", errInvalidOrgCoderConfig, err)
	}
	placement.connectionID = chosen.ID
	placement.override = &sandbox.CoderDeploymentOverride{
		BaseURL:                   cfg.BaseURL,
		Owner:                     cfg.Owner,
		TemplateID:                cfg.TemplateID,
		AgentName:                 cfg.AgentName,
		Parameters:                cfg.Parameters,
		DurableRoot:               cfg.DurableRoot,
		RequireMountedDurableRoot: cfg.RequireMountedDurableRoot,
		StartupTimeoutSeconds:     cfg.StartupTimeoutSeconds,
		WorkerUser:                cfg.WorkerUser,
		ReadyProbe:                cfg.ReadyProbe,
		ReadyProbeTimeoutSeconds:  cfg.ReadyProbeTimeoutSeconds,
		ReuseExistingCheckout:     cfg.ReusesExistingCheckout(),
		WorkingDirectory:          cfg.WorkingDirectory,
		ProtectSecretFiles:        cfg.ProtectsSecretFiles(),
	}
	// A project's own working directory (for example ~/work/{repo}) wins over
	// the connection's, since where repositories live can differ per project.
	// Like the connection's, it only applies when AO runs as the workspace user.
	if directory := domain.ProjectWorkingDirectory(projectConfig); directory != "" &&
		cfg.WorkerUser == domain.CoderWorkerUserWorkspace {
		placement.override.WorkingDirectory = directory
	}
	return placement, nil
}

// orgCoderPlacement resolves the Coder placement for a request-driven session
// in orgID (top-level and scratch sessions), reading the organization's
// provider connections as the principal.
func (s *Server) orgCoderPlacement(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
	projectConfig json.RawMessage,
) (coderPlacement, error) {
	var connections []domain.ProviderConnection
	if store, ok := s.store.(providerConnectionStore); ok {
		var err error
		connections, err = store.ListProviderConnections(ctx, principal, orgID)
		if err != nil {
			return coderPlacement{}, err
		}
	}
	return resolveCoderPlacement(projectConfig, connections, "")
}

// writeSessionPlanError reports a failure to resolve a session's provisioning
// plan. A bring-your-own-Coder organization with no org-default template must
// pick one per project; that is user-fixable, so it is a clear 422 rather than
// a deployment-misconfiguration 500.
func (s *Server) writeSessionPlanError(w http.ResponseWriter, r *http.Request, orgID, projectID string, err error) {
	if errors.Is(err, sandbox.ErrCoderTemplateRequired) {
		s.logger.Warn(
			"session create rejected: coder template required",
			"org_id", orgID,
			"user_id", principalFrom(r).UserID,
			"project_id", projectID,
		)
		s.recordActivity(r, domain.ActivityEvent{OrgID: orgID, Kind: domain.ActivitySessionCreateRefused,
			Provider: sandbox.ProviderCoder, Attrs: map[string]any{
				"code": "coder_template_required", "project_id": projectID,
			}})
		writeError(
			w, r, http.StatusUnprocessableEntity, "coder_template_required",
			"Choose a Coder template for this project before starting a session.",
		)
		return
	}
	if errors.Is(err, sandbox.ErrCoderTemplateUnavailable) {
		s.logger.Warn(
			"session create rejected: coder template unavailable",
			"org_id", orgID,
			"user_id", principalFrom(r).UserID,
			"project_id", projectID,
		)
		s.recordActivity(r, domain.ActivityEvent{OrgID: orgID, Kind: domain.ActivitySessionCreateRefused,
			Provider: sandbox.ProviderCoder, Attrs: map[string]any{
				"code": "coder_template_unavailable", "project_id": projectID,
			}})
		writeError(
			w, r, http.StatusUnprocessableEntity, "coder_template_unavailable",
			"This project's Coder template is not available. Choose another template in the project's settings.",
		)
		return
	}
	s.logger.Error("resolve sandbox provisioning plan", "error", err, "request_id", requestID(r))
	writeError(
		w, r, http.StatusInternalServerError, "internal_error",
		"Sandbox provisioning is misconfigured on this deployment.",
	)
}

// isScratchProject reports whether a project was created as a scratch project
// (no repository chosen), whose config carries source "scratch".
func isScratchProject(config json.RawMessage) bool {
	var probe struct {
		Source string `json:"source"`
	}
	return json.Unmarshal(config, &probe) == nil && probe.Source == "scratch"
}

// scratchSessionPlan resolves where a new scratch project's orchestrator runs,
// with the same rules as any other session: the project config's
// sandboxProvider when it sets one (which the organization must be allowed to
// use), otherwise the organization's default provider, and for Coder the
// organization's bring-your-own deployment. It writes the error response and
// returns false on failure; on success it returns the plan and the provider
// connection the session binds to ("" for the deployment default).
func (s *Server) scratchSessionPlan(
	w http.ResponseWriter,
	r *http.Request,
	principal domain.Principal,
	orgID, harness string,
	projectConfig json.RawMessage,
) (sandbox.Plan, string, bool) {
	provider := projectProvider(projectConfig)
	if provider != "" {
		if !s.orgProviderAvailable(principal, orgID, provider) {
			writeError(
				w, r, http.StatusUnprocessableEntity, "provider_unavailable",
				"The project's sandbox provider is not available to your organization.",
			)
			return sandbox.Plan{}, "", false
		}
	} else if provider = s.defaultProviderForOrg(r.Context(), principal, orgID); provider == "" {
		writeProviderForbidden(w, r, "", false)
		return sandbox.Plan{}, "", false
	}
	var placement coderPlacement
	if provider == sandbox.ProviderCoder {
		var err error
		placement, err = s.orgCoderPlacement(r.Context(), principal, orgID, projectConfig)
		if err != nil {
			s.writeCoderPlacementError(w, r, err)
			return sandbox.Plan{}, "", false
		}
	}
	plan, err := s.provisioning.SessionPlanForProviderWithCoder(harness, provider, placement.options, placement.override)
	if errors.Is(err, sandbox.ErrCoderTemplateRequired) && placement.connectionID != "" {
		// A scratch project has no way to pick a template, so when the org's own
		// Coder connection has no default one the orchestrator runs on the
		// deployment's Coder, as it did before scratch projects used BYO placement.
		placement = coderPlacement{}
		plan, err = s.provisioning.SessionPlanForProviderWithCoder(harness, provider, placement.options, placement.override)
	}
	if err != nil {
		s.writeSessionPlanError(w, r, orgID, "", err)
		return sandbox.Plan{}, "", false
	}
	return plan, placement.connectionID, true
}
