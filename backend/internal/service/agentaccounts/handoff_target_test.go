package agentaccounts

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// handoffLimitsProvider answers each folder's limits from a fixed table; reads
// run in parallel, so it holds no mutable state.
type handoffLimitsProvider struct {
	*fakeClaude
	used map[string]float64
}

func (p *handoffLimitsProvider) Limits(_ context.Context, home string) (domain.AgentAccountLimits, error) {
	return domain.AgentAccountLimits{Windows: []domain.AgentAccountLimitWindow{
		{ID: "five_hour", UsedPercent: p.used[home]},
		{ID: "seven_day_opus", UsedPercent: 100, Scope: "opus"},
	}}, nil
}

func TestHandoffTargetPicksMostVerifiedRoomForTheModel(t *testing.T) {
	store := &memStore{
		accounts: []domain.AgentAccount{
			{ID: "a", Provider: domain.AgentAccountProviderClaude, Home: "/a", Email: "a@example.com"},
			{ID: "b", Provider: domain.AgentAccountProviderClaude, Home: "/b", Email: "b@example.com"},
			{ID: "c", Provider: domain.AgentAccountProviderClaude, Home: "/c", Email: "c@example.com"},
			{ID: "full", Provider: domain.AgentAccountProviderClaude, Home: "/full", Email: "full@example.com"},
			{ID: "out", Provider: domain.AgentAccountProviderClaude, Home: "/out", Email: "out@example.com"},
			{ID: "other", Provider: domain.AgentAccountProviderClaude, Home: "/other", Email: "other@example.com"},
		},
		defaults: map[domain.AgentAccountProvider]string{},
		bindings: map[domain.SessionID]string{"session": "a"},
	}
	provider := &handoffLimitsProvider{
		fakeClaude: &fakeClaude{
			signedInAs: map[string]string{
				"/a": "a@example.com", "/b": "b@example.com", "/c": "c@example.com",
				"/full": "full@example.com", "/other": "someone-else@example.com",
			},
			failing: map[string]bool{},
		},
		used: map[string]float64{"/a": 0, "/b": 50, "/c": 20, "/full": 95, "/out": 0, "/other": 0},
	}
	svc := New(store, []ports.AgentAccountProviderCLI{provider}, t.TempDir(), nil, nil)
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		model   string
		exclude map[string]bool
		want    string
	}{
		// The current account, a signed-out one, one signed in as someone else,
		// and one near its limit never qualify.
		{"most room wins", "claude-sonnet-5", nil, "c"},
		{"excluded account is skipped", "claude-sonnet-5", map[string]bool{"c": true}, "b"},
		{"the model's full meter rules every account out", "claude-opus-5", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.HandoffTarget(ctx, "session", domain.HarnessClaudeCode, tc.model, tc.exclude)
			if err != nil || got != tc.want {
				t.Fatalf("HandoffTarget = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := svc.HandoffTarget(ctx, "session", domain.HarnessCodex, "", nil); err == nil {
		t.Fatal("a harness without this provider picked a target")
	}
}
