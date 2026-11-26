package opencodev2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// The first task waits for AO's agent whenever AO selects one, because the
// OpenCode 2 TUI submits --prompt before background config agents load (#9067).
func TestPromptDeliveryWaitsForAOAgentOnlyWhenOneIsSelected(t *testing.T) {
	file := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(file, []byte("file rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cfg  ports.LaunchConfig
		want ports.PromptDeliveryStrategy
	}{
		{"no AO agent", ports.LaunchConfig{Prompt: "task"}, ports.PromptDeliveryInCommand},
		{"auto mode only", ports.LaunchConfig{Prompt: "task", Permissions: ports.PermissionModeAuto}, ports.PromptDeliveryInCommand},
		{"empty prompt file", ports.LaunchConfig{Prompt: "task", SystemPromptFile: empty}, ports.PromptDeliveryInCommand},
		{"blank model", ports.LaunchConfig{Prompt: "task", Config: ports.AgentConfig{Model: "  "}}, ports.PromptDeliveryInCommand},
		{"system prompt", ports.LaunchConfig{Prompt: "task", SystemPrompt: "rules"}, ports.PromptDeliveryAfterStart},
		{"prompt file", ports.LaunchConfig{Prompt: "task", SystemPromptFile: file}, ports.PromptDeliveryAfterStart},
		{"model", ports.LaunchConfig{Prompt: "task", Config: ports.AgentConfig{Model: "router/router-agility"}}, ports.PromptDeliveryAfterStart},
		{"accept edits", ports.LaunchConfig{Prompt: "task", Permissions: ports.PermissionModeAcceptEdits}, ports.PromptDeliveryAfterStart},
		{"bypass", ports.LaunchConfig{Prompt: "task", Permissions: ports.PermissionModeBypassPermissions}, ports.PromptDeliveryAfterStart},
	} {
		got, err := New().GetPromptDeliveryStrategy(context.Background(), tc.cfg)
		if err != nil || got != tc.want {
			t.Errorf("%s: strategy = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if _, err := New().GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{SystemPromptFile: filepath.Join(t.TempDir(), "missing.md")}); err == nil {
		t.Fatal("unreadable system prompt file: want error")
	}
}

func TestPromptReadinessWaitsForTheAgentLabelOnTheRenderedScreen(t *testing.T) {
	hints, err := New().PromptReadinessHints(context.Background(), ports.LaunchConfig{SessionID: "sess/1"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hints.Patterns, []string{"Ao-Sess-1"}) || !slices.Equal(hints.BlockingPatterns, []string{"No provider selected"}) || !hints.RenderedScreen || hints.Timeout <= 0 || hints.RequireReady {
		t.Fatalf("hints = %+v", hints)
	}
}

// agentLabel matches OpenCode's Locale.titlecase: str.replace(/\b\w/g, upper).
func TestAgentLabelMatchesTheTUITitlecase(t *testing.T) {
	for name, want := range map[string]string{
		"ao-w-1":           "Ao-W-1",
		"ao-mer-12":        "Ao-Mer-12",
		"ao-my_sess":       "Ao-My_sess",
		"ao-1abc":          "Ao-1abc",
		"ao-Mixed-Case":    "Ao-Mixed-Case",
		"ao-system-prompt": "Ao-System-Prompt",
	} {
		if got := agentLabel(name); got != want {
			t.Errorf("agentLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestTypedFirstTaskTurnsOffReleaseHighlights(t *testing.T) {
	fakeBinary(t, "2.0.0")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	t.Setenv(cliConfigEnv, `{"theme":"mine","highlights":true}`)
	cfg := ports.LaunchConfig{SessionID: "w-1", SystemPrompt: "rules"}

	// The manager clears Prompt for after-start delivery.
	cmd, err := New().GetLaunchCommand(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cli := envValue(cmd, cliConfigEnv)
	var got map[string]any
	if err := json.Unmarshal([]byte(cli), &got); err != nil {
		t.Fatalf("%s = %q: %v", cliConfigEnv, cli, err)
	}
	if got["highlights"] != false || got["theme"] != "mine" {
		t.Fatalf("%s = %s, want user keys kept and highlights off", cliConfigEnv, cli)
	}
	if slices.ContainsFunc(cmd, func(arg string) bool { return strings.HasPrefix(arg, "--prompt") }) {
		t.Fatalf("after-start launch still passes --prompt: %#v", cmd)
	}

	// A --prompt launch, a native restore, and a launch without an AO agent keep
	// the user's TUI settings untouched.
	withPrompt := cfg
	withPrompt.Prompt = "task"
	cmd, err = New().GetLaunchCommand(context.Background(), withPrompt)
	if err != nil || envValue(cmd, cliConfigEnv) != "" {
		t.Fatalf("--prompt launch = %#v, %v", cmd, err)
	}
	cmd, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{SystemPrompt: "rules", Session: ports.SessionRef{ID: "w-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ses_native"}}})
	if err != nil || !ok || envValue(cmd, cliConfigEnv) != "" {
		t.Fatalf("restore = %#v, %v, %v", cmd, ok, err)
	}
	cmd, err = New().GetLaunchCommand(context.Background(), ports.LaunchConfig{SessionID: "w-1"})
	if err != nil || envValue(cmd, cliConfigEnv) != "" {
		t.Fatalf("no-agent launch = %#v, %v", cmd, err)
	}

	t.Setenv(cliConfigEnv, "[1]")
	if _, err := New().GetLaunchCommand(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), cliConfigEnv) {
		t.Fatalf("invalid %s: err = %v", cliConfigEnv, err)
	}
}

func envValue(cmd []string, key string) string {
	for _, arg := range cmd {
		if value, ok := strings.CutPrefix(arg, key+"="); ok {
			return value
		}
	}
	return ""
}
