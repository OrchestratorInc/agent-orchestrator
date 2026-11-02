package aider

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAiderAuthRecordsTheDecidingEnvironmentVariable(t *testing.T) {
	// No Aider config or .env file: the environment is the fallback.
	t.Setenv("HOME", t.TempDir())
	for _, name := range aiderAPIKeyEnvVars {
		t.Setenv(name, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, ok, err := aiderLocalAuthStatus(ctx)
	if err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v, %v)", status, ok, err)
	}
	want := domain.EnvironmentSource("ANTHROPIC_API_KEY").WithMethod(domain.AgentAccountMethodAPIKey)
	if got := evidence.Identity(); got != want {
		t.Fatalf("evidence = %+v, want %+v", got, want)
	}
}
