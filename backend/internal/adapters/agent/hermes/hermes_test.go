package hermes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandingInstructionsPreserveNativeOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("agent:\n  system_prompt: Native profile instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HERMES_EPHEMERAL_SYSTEM_PROMPT": "Native invocation instructions"}
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO standing instructions"}
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]; got != "Native invocation instructions\n\nAO standing instructions" {
		t.Fatalf("got %q", got)
	}
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Count(env["HERMES_EPHEMERAL_SYSTEM_PROMPT"], "AO standing instructions") != 1 {
		t.Fatal("overlay duplicated")
	}
	env = map[string]string{}
	cfg.Env = env
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env["HERMES_EPHEMERAL_SYSTEM_PROMPT"], "Native profile instructions") {
		t.Fatal("lost native profile")
	}
}
func TestRestoreRefusesUnobservedIdentity(t *testing.T) {
	for _, id := range []string{"", "latest", "some title", "../../state.db"} {
		_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
		if ok || err == nil {
			t.Fatalf("id %q: ok=%v err=%v", id, ok, err)
		}
	}
}
func TestLaunchRejectsUnsupportedPermissionsAndRestrictions(t *testing.T) {
	for _, cfg := range []ports.LaunchConfig{{Permissions: ports.PermissionModeAcceptEdits}, {Permissions: ports.PermissionModeAuto}, {AllowedTools: []string{"read"}}} {
		if _, err := New().GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatal("unsupported control accepted")
		}
	}
}
func TestActivityCurrentComposerAndDecision(t *testing.T) {
	for _, tt := range []struct{ screen, want string }{{"☤ ❯ msg=interrupt · /queue · Ctrl+C cancel", "active"}, {"☤ ❯ \n", "idle"}, {"☤ ❯ \nAllow this command?\n❯ [1] Allow once", "blocked"}, {"unrelated text", ""}} {
		state, ok := New().DetectTerminalActivity(tt.screen)
		if string(state) != tt.want || ok != (tt.want != "") {
			t.Fatalf("%q got %s/%v", tt.screen, state, ok)
		}
	}
}

func TestLeadingDashTaskStaysInPrivateQueryFile(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "hermes"), []byte("#!/bin/sh\necho 'Hermes Agent v0.21.6'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	data := t.TempDir()
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: "/workspace", Prompt: "--yolo is task data"})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range cmd {
		if arg == "--yolo" {
			t.Fatal("task became option")
		}
	}
	b, err := os.ReadFile(filepath.Join(data, "agents", "hermes", "ao-1", "initial-query.txt"))
	if err != nil || string(b) != "--yolo is task data" {
		t.Fatalf("query=%q err=%v", b, err)
	}
}
func TestPersonalityPreservesNativePrecedence(t *testing.T) {
	overlay := nativeOverlay(map[string]any{"display": map[string]any{"personality": "technical"}, "agent": map[string]any{"system_prompt": "ignored native manual overlay"}})
	if overlay != builtinPersonalities["technical"] {
		t.Fatalf("overlay=%q", overlay)
	}
	overlay = nativeOverlay(map[string]any{"display": map[string]any{"personality": "custom"}, "personalities": map[string]any{"custom": "first"}, "agent": map[string]any{"personalities": map[string]any{"custom": map[string]any{"system_prompt": "native", "tone": "gentle"}}}})
	if overlay != "native\nTone: gentle" {
		t.Fatalf("overlay=%q", overlay)
	}
}

func TestNativeModalChromeBlocks(t *testing.T) {
	for _, screen := range []string{"Dangerous Command\n❯ 1. Allow once\n⚠ ❯", "? ❯"} {
		state, ok := New().DetectTerminalActivity(screen)
		if !ok || state != "blocked" {
			t.Fatalf("modal admitted: %q %s", screen, state)
		}
	}
}
func TestStickyProfileFailsClosed(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "active_profile"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	if New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "test", Env: map[string]string{"HERMES_HOME": home}, SystemPrompt: "AO"}) == nil {
		t.Fatal("sticky profile silently overridden")
	}
}

func TestNativeItalicPlaceholderAndDraft(t *testing.T) {
	state, ok := New().DetectTerminalActivity("────────────────────────────────────────\n❯ \x1b[38;5;136;3mPlan a feature\x1b[m\n────────────────────────────────────────")
	if !ok || state != "idle" {
		t.Fatalf("native placeholder %s %v", state, ok)
	}
	if _, ok := New().DetectTerminalActivity("────────────────────────────────────────\n❯ Plan a feature\n────────────────────────────────────────"); ok {
		t.Fatal("human draft accepted")
	}
}

func TestRestoreRefusesNativeRotationAfterObservation(t *testing.T) {
	data := t.TempDir()
	dir := sessionDir(data, "ao-rotation")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "native-home"), []byte(home), 0o600); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(home, "native-terminal-fixture")
	if err := os.WriteFile(crumb, []byte(`{"session_id":"new-conversation","cwd":"/workspace","ts":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "observed-terminal"), []byte(crumb), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-rotation", WorkspacePath: "/workspace", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "old-conversation"}}})
	if err == nil || ok || !strings.Contains(err.Error(), "rotated") {
		t.Fatalf("stale conversation restore: %v %v", ok, err)
	}
}
