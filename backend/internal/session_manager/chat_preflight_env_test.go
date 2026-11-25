package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type preflightEnvAccounts struct {
	*transitionAccounts
	ids []domain.SessionID
}

func (a *preflightEnvAccounts) SessionLaunchEnv(_ context.Context, id domain.SessionID) (map[string]string, error) {
	a.ids = append(a.ids, id)
	return map[string]string{}, nil
}

func (a *preflightEnvAccounts) SessionLaunchEnvWith(_ context.Context, id domain.SessionID, _ string) (map[string]string, error) {
	a.ids = append(a.ids, id)
	return map[string]string{}, nil
}

// Chat admission runs before the session exists. Resolving its account would
// bind the provider's default to the empty session ID, so admission must leave
// account environment to the launch while still carrying the project's keys.
func TestChatPreflightEnvDoesNotResolveAnAccountForTheUnbornSession(t *testing.T) {
	manager, _, _ := newExitedResumeManager(t, &fakeRuntime{}, &importEnvAgent{})
	accounts := &preflightEnvAccounts{transitionAccounts: newTransitionAccounts(new([]string))}
	manager.agentAccounts = accounts
	for _, accountID := range []string{"", "acct-b"} {
		cfg := ports.SpawnConfig{ProjectID: "mer", Harness: domain.HarnessDockerAgent, AgentAccountID: accountID}
		env, err := manager.chatPreflightEnv(cfg, map[string]string{"OPENAI_API_KEY": "project-key"})
		if err != nil {
			t.Fatal(err)
		}
		if env["OPENAI_API_KEY"] != "project-key" {
			t.Fatalf("account %q: preflight env lost the project key: %v", accountID, env)
		}
	}
	if len(accounts.ids) != 0 {
		t.Fatalf("preflight resolved accounts for session IDs %q; it must not bind the unborn session", accounts.ids)
	}
}
