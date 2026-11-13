package agentaccounts

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type handoffLimitsProvider struct {
	*fakeClaude
	used     map[string]float64
	onLimits func()
}

func (p *handoffLimitsProvider) Limits(_ context.Context, home string) (domain.AgentAccountLimits, error) {
	if p.onLimits != nil {
		p.onLimits()
	}
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
		name  string
		model string
		used  map[string]float64
		want  string
	}{
		{"most room wins", "claude-sonnet-5", map[string]float64{"/b": 50, "/c": 20, "/full": 95}, "c"},
		{"a nearly full account is picked when nothing has more room", "claude-sonnet-5", map[string]float64{"/b": 100, "/c": 100, "/full": 95}, "full"},
		{"the model's full meter rules every account out", "claude-opus-5", map[string]float64{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			maps.Copy(provider.used, tc.used)
			got, err := svc.HandoffTarget(ctx, "session", domain.HarnessClaudeCode, tc.model)
			if err != nil || got != tc.want {
				t.Fatalf("HandoffTarget = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := svc.HandoffTarget(ctx, "session", domain.HarnessCodex, ""); err == nil {
		t.Fatal("a harness without this provider picked a target")
	}
	var moved sync.Once
	provider.onLimits = func() { moved.Do(func() { store.bindings["session"] = "b" }) }
	if got, err := svc.HandoffTarget(ctx, "session", domain.HarnessClaudeCode, "claude-sonnet-5"); !errors.Is(err, ports.ErrAgentAccountChanged) {
		t.Fatalf("a move during the probes: target %q, err %v", got, err)
	}
}
