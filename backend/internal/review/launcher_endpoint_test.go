package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
// A reviewer for a project that sets only its own gateway URL must not start:
// the saved key is never sent to a different origin, and no inherited
// credential goes in its place. Nothing is started or replaced.
func TestLauncherStopsOnIncompleteProjectGatewayOverride(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net"}
	if _, err := l.Spawn(context.Background(), spec); !errors.Is(err, harnessendpoint.ErrIncompleteOverride) {
		t.Fatalf("Spawn: err = %v, want an incomplete override", err)
	}
	if _, err := l.RestoreTerminal(context.Background(), spec); !errors.Is(err, harnessendpoint.ErrIncompleteOverride) {
		t.Fatalf("RestoreTerminal: err = %v, want an incomplete override", err)
	}
	if rt.created || rt.destroyed != "" {
		t.Fatal("an incomplete override must stop before the runtime is touched")
	}
}

// A complete project gateway override (its own URL and key on another origin)
// reaches the reviewer with only the project's key: neither the saved key nor
// an inherited credential rides along.
func TestLauncherUsesCompleteProjectGatewayOverride(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reviewer := &fakeReviewer{env: map[string]string{"ANTHROPIC_API_KEY": "sk-inherited", "CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-inherited"}}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net", "ANTHROPIC_AUTH_TOKEN": "sk-project"}
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := rt.createCfg.Env
	if env["ANTHROPIC_BASE_URL"] != "https://other.example.net" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-project" {
		t.Fatalf("project gateway not applied: url=%q", env["ANTHROPIC_BASE_URL"])
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s must be blank beside the project's key (set=%v)", key, ok)
		}
	}
	for key, value := range env {
		if value == "sk-endpoint" {
			t.Fatalf("saved endpoint key reached the project's gateway via %s", key)
		}
	}
}

// An endpoint that cannot be read stops the reviewer before anything is
// started or replaced.
func TestLauncherRejectsBrokenEndpointBeforeStartingOrReplacingProcess(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "harness-endpoints", "claude-code", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	if _, err := l.Spawn(context.Background(), launchSpec()); err == nil {
		t.Fatal("spawn accepted an unreadable endpoint")
	}
	if _, err := l.RestoreTerminal(context.Background(), launchSpec()); err == nil {
		t.Fatal("restore accepted an unreadable endpoint")
	}
	if rt.created || rt.destroyed != "" {
		t.Fatal("an endpoint error must be returned before touching the runtime")
	}
}

// The reviewer keeps the endpoint's cleared variables blank even when the
// project sets them, like a worker session.
func TestLauncherKeepsClearedVariablesBlankOverProjectEnv(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-project"}
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := rt.createCfg.Env
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s = %q (set=%v), want blank while the custom endpoint is active", key, value, ok)
		}
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("endpoint key = %q, want the saved key", env["ANTHROPIC_AUTH_TOKEN"])
	}
}
