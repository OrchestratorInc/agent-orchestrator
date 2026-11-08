package agentaccounts

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDefaultLaunchEnvFollowsNewTaskAccount(t *testing.T) {
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
	assertHome := func(want string) {
		t.Helper()
		env, err := svc.DefaultLaunchEnv(ctx, domain.HarnessClaudeCode)
		if err != nil || env["CLAUDE_CONFIG_DIR"] != want {
			t.Fatalf("discovery home = %q, error = %v; want %q", env["CLAUDE_CONFIG_DIR"], err, want)
		}
		if len(store.bindings) != 0 {
			t.Fatal("discovery bound a session")
		}
	}
	assertHome("/accounts/team")
	if err := svc.SetDefault(ctx, "max"); err != nil {
		t.Fatal(err)
	}
	assertHome("/accounts/max")
	delete(provider.signedInAs, "/accounts/max")
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	assertHome("/accounts/team")
	delete(provider.signedInAs, "/accounts/team")
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DefaultLaunchEnv(ctx, domain.HarnessClaudeCode); !errors.Is(err, ports.ErrAgentAccountDefaultUnusable) {
		t.Fatalf("unusable accounts error = %v", err)
	}
}

func TestDefaultLaunchEnvLeavesComputerLoginUnchanged(t *testing.T) {
	svc := New(&memStore{}, []ports.AgentAccountProviderCLI{&fakeClaude{}}, t.TempDir(), nil, nil)
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex, "other"} {
		env, err := svc.DefaultLaunchEnv(context.Background(), harness)
		if err != nil || len(env) != 0 {
			t.Fatalf("computer login env = %v, error = %v", env, err)
		}
	}
}
