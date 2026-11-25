package review

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

// A Claude reviewer under a custom endpoint must not carry a subscription
// token or a provider switch beside it: Claude Code prefers both over the
// endpoint's key and base URL.
func TestLauncherSpawnClearsCompetingClaudeCredentialsForCustomEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reviewer := &fakeReviewer{env: map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-inherited",
		"CLAUDE_CODE_USE_BEDROCK": "1",
	}}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, dataDir)
	if _, err := l.Spawn(context.Background(), launchSpec()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := rt.createCfg.Env
	if env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("custom endpoint not applied to the reviewer: %v", env)
	}
	for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_API_KEY"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s = %q (set=%v), want blanked beside the custom endpoint", key, value, ok)
		}
	}
}

// A project that sets only its own gateway URL gets a reviewer without the
// saved endpoint's key: the key is never sent to a different origin.
func TestLauncherSpawnKeepsEndpointKeyOffAProjectGateway(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net"}
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	for key, value := range rt.createCfg.Env {
		if value == "sk-endpoint" {
			t.Fatalf("saved endpoint key reached the project's gateway via %s", key)
		}
	}
}
