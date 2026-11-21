package agent

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

// authenticationOverride answers readiness for a harness the user pointed at
// their own API endpoint before any other check: AO injects that base URL and
// key into every session of the harness, so the harness's own login no longer
// decides how it authenticates. AO cannot prove an arbitrary gateway accepts
// the key, so the state is configured, not authorized. Everything else keeps
// the account-aware check.
func (s *Service) authenticationOverride(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	if s.dataDir != "" {
		// A read failure falls through to the usual checks rather than
		// reporting a harness signed out that may well work.
		if configured, err := harnessendpoint.ForDataDir(s.dataDir).Configured(agentID); err == nil && configured {
			return successfulAuthentication(s.now(), domain.AgentAuthenticationConfigured, domain.AgentReadinessReasonAuthConfigured,
				"Using a custom API endpoint. Orchestrator has not verified the key."), true
		}
	}
	return s.authenticationWithAccount(ctx, agentID, purpose)
}
