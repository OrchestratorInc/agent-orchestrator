package strands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestHooksPreservePluginsAndStayOutsideWorkspace(t *testing.T) {
	p, cfg := setup(t, `{"profile":{"plugins":[{"kind":"plugin","module":"./user.mjs"}]}}`)
	cmd, err := p.GetLaunchCommand(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "./user.mjs") || !strings.Contains(joined, hookPath(cfg.DataDir)) {
		t.Fatal(cmd)
	}
	before, err := os.ReadFile(hookPath(cfg.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(hookPath(cfg.DataDir))
	if string(before) != string(after) {
		t.Fatal("not idempotent")
	}
	entries, _ := os.ReadDir(cfg.WorkspacePath)
	if len(entries) != 0 {
		t.Fatal("workspace mutated")
	}
	if filepath.Dir(hookPath(cfg.DataDir)) == cfg.WorkspacePath {
		t.Fatal("hook in workspace")
	}
}

func TestActivityUsesInvocationCallbacks(t *testing.T) {
	for event, want := range map[string]domain.ActivityState{"session-start": domain.ActivityIdle, "active": domain.ActivityActive, "stop": domain.ActivityIdle} {
		got, ok := DeriveActivityState(event, nil)
		if !ok || got != want {
			t.Fatalf("%s: %s %v", event, got, ok)
		}
	}
	if _, ok := DeriveActivityState("before-tool", nil); ok {
		t.Fatal("tool is not an invocation")
	}
}

func TestComposerRequiresEmptyEditorAndFooter(t *testing.T) {
	p := New()
	ready := "▌Enter to send • Ctrl+J for newline • / for commands\n\nmodel • Auto /workspace /settings"
	if !p.ComposerIsEmpty(ready) {
		t.Fatal("ready editor not recognized")
	}
	for _, input := range []string{"", "Loading…", strings.ReplaceAll(ready, "▌Enter to send • Ctrl+J for newline • / for commands", "draft text"), ready + "\nPermission required", "▌Enter to send • Ctrl+J for newline • / for commands"} {
		if p.ComposerIsEmpty(input) {
			t.Fatalf("not ready: %q", input)
		}
	}
}

func TestAuthDoesNotClaimAuthorization(t *testing.T) {
	p, _ := setup(t, `{"profile":{"model":"litellm/fixture"}}`)
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "LITELLM_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(key, "")
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("%s %v", status, err)
	}
	t.Setenv("LITELLM_API_KEY", "fixture-not-a-real-secret")
	status, err = p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("%s %v", status, err)
	}
}
