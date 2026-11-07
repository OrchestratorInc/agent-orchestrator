package coderagents

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus verifies the native coder session against the deployment.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	return p.AuthStatusWithEnv(ctx, nil)
}

// AuthStatusWithEnv checks the exact environment a session would use,
// including project-scoped CODER_URL / CODER_SESSION_TOKEN overrides.
//
// Authorized needs a provider round trip: the session is accepted
// (GET /users/me) and the user's organization offers at least one enabled
// Coder Agents model. A session that exists but cannot be verified is only
// configured; a rejected or missing session is unauthorized.
func (p *Plugin) AuthStatusWithEnv(ctx context.Context, env map[string]string) (ports.AgentAuthStatus, error) {
	client, creds, err := p.Connect(ctx, env)
	if errors.Is(err, coderapi.ErrNotLoggedIn) {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	evidence := creds.Source
	evidence.Organization = creds.URL.Host
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	user, err := client.Me(requestCtx)
	switch status := coderapi.StatusCode(err); {
	case err == nil:
	case status == http.StatusUnauthorized:
		return ports.AgentAuthStatusUnauthorized, nil
	default:
		ports.RecordAuthEvidence(ctx, evidence)
		return ports.AgentAuthStatusConfigured, nil
	}
	evidence.Name = strings.TrimSpace(user.Username)
	evidence.Email = strings.TrimSpace(user.Email)
	org, err := coderapi.ResolveOrganization(requestCtx, client, Organization(env))
	if err != nil {
		ports.RecordAuthEvidence(ctx, evidence)
		return ports.AgentAuthStatusConfigured, nil
	}
	if name := strings.TrimSpace(org.DisplayName); name != "" {
		evidence.Organization = name
	} else if org.Name != "" {
		evidence.Organization = org.Name
	}
	models, err := client.ChatModels(requestCtx, org.ID)
	ports.RecordAuthEvidence(ctx, evidence)
	if err != nil || len(coderapi.EnabledModels(models)) == 0 {
		// The session works, but this deployment cannot run Coder Agents for
		// the user yet (no stable chats API, no enabled model, or no access).
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusAuthorized, nil
}
