package httpapi

import (
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// orgAllowsProvider reports whether the principal's active organization may use
// the given sandbox provider. A provider that is not capability-gated (see
// AO_CLOUD_CAPABILITY_GATED_PROVIDERS) is always allowed; a gated provider
// requires the organization to hold a capability of the same name, seeded from
// WorkOS org metadata (auth.parseOrganizationCapabilities). With no gated
// providers configured, every provider is allowed and behavior is unchanged,
// which is what keeps rollout safe: ship the code, flag the entitled orgs in
// WorkOS, then set AO_CLOUD_CAPABILITY_GATED_PROVIDERS to turn the gate on.
func (s *Server) orgAllowsProvider(principal domain.Principal, provider string) bool {
	if !s.capabilityGatedProviders[provider] {
		return true
	}
	return slices.Contains(principal.OrgCapabilities, provider)
}

// providerForOrg decides which sandbox provider a session for this principal's
// organization runs on, from server-side data only. The per-org override comes
// from WorkOS org metadata (metadata.sandbox_provider, surfaced as
// principal.SandboxProvider); it wins when set and offered by this deployment.
// Otherwise the org takes the deployment default (what the general public gets).
// The routing is provider-agnostic: adding a provider later is a metadata/env
// change, never a change here.
func (s *Server) providerForOrg(principal domain.Principal) string {
	if p := strings.TrimSpace(principal.SandboxProvider); p != "" && slices.Contains(s.availableSandboxProviders, p) {
		return p
	}
	return s.sandboxProvider // deployment default
}

// providersForOrg filters the offered providers down to those the principal's
// organization is entitled to use, so the client never sees a provider the
// server would refuse on create.
func (s *Server) providersForOrg(principal domain.Principal, available []string) []string {
	allowed := make([]string, 0, len(available))
	for _, provider := range available {
		if s.orgAllowsProvider(principal, provider) {
			allowed = append(allowed, provider)
		}
	}
	return allowed
}
