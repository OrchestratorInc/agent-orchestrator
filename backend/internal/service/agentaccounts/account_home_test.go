package agentaccounts

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AccountHome names the home a spawn's CLI would run under, for a chosen
// account or the default, without binding anything (#4518 scopes Codex model
// refusals by it).
func TestAccountHomeResolvesTheChosenOrDefaultAccountWithoutBinding(t *testing.T) {
	ctx := context.Background()
	store := &memStore{
		accounts: []domain.AgentAccount{
			{ID: "team", Provider: domain.AgentAccountProviderClaude, Home: "/accounts/team", Email: "team@example.com"},
			{ID: "max", Provider: domain.AgentAccountProviderClaude, Home: "/accounts/max", Email: "max@example.com"},
		},
		defaults: map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "team"},
		bindings: map[domain.SessionID]string{},
	}
	provider := &fakeClaude{signedInAs: map[string]string{"/accounts/team": "team@example.com", "/accounts/max": "max@example.com"}}
	svc := New(store, []ports.AgentAccountProviderCLI{provider}, t.TempDir(), nil, nil)

	for accountID, want := range map[string]string{"": "/accounts/team", "max": "/accounts/max"} {
		home, err := svc.AccountHome(ctx, domain.HarnessClaudeCode, accountID)
		if err != nil || home != want {
			t.Fatalf("AccountHome(%q) = %q, %v; want %q", accountID, home, err, want)
		}
	}
	if _, err := svc.AccountHome(ctx, domain.HarnessClaudeCode, "gone"); !errors.Is(err, ports.ErrAgentAccountUnknown) {
		t.Fatalf("AccountHome(unknown) error = %v, want ErrAgentAccountUnknown", err)
	}
	if home, err := svc.AccountHome(ctx, domain.HarnessAider, ""); err != nil || home != "" {
		t.Fatalf("harness without accounts = %q, %v; want the computer's own", home, err)
	}
	if len(store.bindings) != 0 {
		t.Fatal("AccountHome bound a session")
	}
}
