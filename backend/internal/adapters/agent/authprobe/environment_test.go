package authprobe

import (
	"context"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestChecksSeeTheHarnessEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ao-saved-key")
	ctx := ports.WithAuthEnvironment(context.Background(), map[string]string{"OPENAI_API_KEY": ""})
	if got := Getenv(ctx, "OPENAI_API_KEY"); got != "" {
		t.Fatalf("Getenv = %q, want the held-back key unset", got)
	}
	if got := Getenv(context.Background(), "OPENAI_API_KEY"); got != "ao-saved-key" {
		t.Fatalf("Getenv without overrides = %q", got)
	}
	if name := CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "OPENAI_API_KEY"); name != "" {
		t.Fatalf("CredentialEnvironment relied on a held-back key: %q", name)
	}
	env := Environ(ctx, []string{"PATH=/bin", "OPENAI_API_KEY=ao-saved-key"})
	if !slices.Equal(env, []string{"PATH=/bin", "OPENAI_API_KEY="}) {
		t.Fatalf("Environ = %q", env)
	}
}
