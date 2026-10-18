package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// Per-role provider pinning (issue #6156). The pin vocabulary and env mapping
// live in domain + agentcreds; this file only knows which role a session kind
// maps to and where the pin joins the launch env.

// RoleProviderPin returns the provider pin configured for the session's role.
// Exported for the session service's staleness resolution, which must fold the
// same pin into its "what would a relaunch resolve now" comparison.
func RoleProviderPin(kind domain.SessionKind, cfg domain.ProjectConfig) string {
	if kind == domain.KindOrchestrator {
		return cfg.Orchestrator.Provider
	}
	return cfg.Worker.Provider
}

// applyRoleProviderPin overlays the role's provider pin onto the launch env so
// the agent process resolves the pinned provider; a pinless role falls through
// to the stored default gateway entry, if one is configured. With neither, the
// launch keeps the settings-chain behavior. Callers inject before the provider
// stamp is taken, so staleness compares like against like.
func (m *Manager) applyRoleProviderPin(ctx context.Context, env map[string]string, projectID string, kind domain.SessionKind, cfg domain.ProjectConfig) {
	if env == nil {
		return
	}
	var entries []agentcreds.GatewayEntry
	if m.providerEntries != nil {
		entries = m.providerEntries(ctx)
	}
	for key, value := range agentcreds.ProviderLaunchEnv(RoleProviderPin(kind, cfg), projectID, entries) {
		env[key] = value
	}
}
