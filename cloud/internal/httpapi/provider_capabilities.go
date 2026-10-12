package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// orgBillingStandingStore reads an organization's plan and grandfathered
// status for domain.BillingMode. The Postgres store implements it; a store
// without it reports no plan and no grandfathering.
type orgBillingStandingStore interface {
	OrgBillingStanding(ctx context.Context, principal domain.Principal, orgID string) (string, bool, error)
}

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

// orgBillingArrangement returns the organization's billing arrangement
// (domain.OrgBillingContract or domain.OrgBillingSelfServe), with the same
// scoping rule as orgCapabilities.
func (s *Server) orgBillingArrangement(principal domain.Principal, orgID string) string {
	if orgID != "" && orgID == principal.TeamOrgID && principal.OrgBilling != "" {
		return principal.OrgBilling
	}
	if s.defaultOrgBilling != "" {
		return s.defaultOrgBilling
	}
	return domain.OrgBillingSelfServe
}

// orgAllowsProvider reports whether an organization may use the given sandbox
// provider. A provider that is not capability-gated (see
// AO_CLOUD_CAPABILITY_GATED_PROVIDERS) is always allowed; a gated provider
// requires the organization to hold a capability of the same name (see
// orgCapabilities). With no gated providers configured, every provider is
// allowed and behavior is unchanged. It does not check that the deployment
// offers the provider; providersForOrg does both.
func (s *Server) orgAllowsProvider(principal domain.Principal, orgID, provider string) bool {
	if !s.capabilityGatedProviders[provider] {
		return true
	}
	return slices.Contains(s.orgCapabilities(principal, orgID), provider)
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

// defaultProviderForOrg is the provider a session gets when nothing chose one:
// the deployment default when the organization may use it, otherwise the
// organization's first allowed provider, otherwise "" (no cloud sessions).
func (s *Server) defaultProviderForOrg(principal domain.Principal, orgID string) string {
	allowed := s.providersForOrg(principal, orgID)
	if slices.Contains(allowed, s.sandboxProvider) {
		return s.sandboxProvider
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

// billingModeForOrg computes domain.BillingMode for one organization.
func (s *Server) billingModeForOrg(ctx context.Context, principal domain.Principal, orgID string) (string, error) {
	if !s.billingReady() || !s.orgOffersBilledProvider(principal, orgID) {
		// Plans only sell billed (Freestyle) capacity: an organization offered
		// none has nothing to buy, matching GET /billing.
		return domain.BillingModeOff, nil
	}
	plan, grandfathered := "", false
	if store, ok := s.store.(orgBillingStandingStore); ok && orgID != "" {
		var err error
		plan, grandfathered, err = store.OrgBillingStanding(ctx, principal, orgID)
		if err != nil {
			return "", err
		}
	}
	return domain.BillingMode(s.orgBillingArrangement(principal, orgID), plan, grandfathered, true), nil
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
}

func (s *Server) offeringForOrg(ctx context.Context, principal domain.Principal, orgID string) (offeringResponse, error) {
	providers := s.providersForOrg(principal, orgID)
	mode, err := s.billingModeForOrg(ctx, principal, orgID)
	if err != nil {
		return offeringResponse{}, err
	}
	return offeringResponse{
		Providers:       providers,
		DefaultProvider: s.defaultProviderForOrg(principal, orgID),
		BillingMode:     mode,
		Kind:            domain.OfferingKind(providers),
		// Plan feature flags arrive with the paywall; none are gated yet.
		Features: []string{},
	}, nil
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
	provider := domain.DecodeProjectSandboxProvider(projectConfig)
	if provider != "" {
		if !s.validateProjectSandboxProvider(w, r, principal, orgID, projectConfig) {
			return sandbox.Plan{}, "", false
		}
	} else if provider = s.defaultProviderForOrg(principal, orgID); provider == "" {
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
