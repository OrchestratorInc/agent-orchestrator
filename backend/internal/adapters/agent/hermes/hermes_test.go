package hermes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestResolveBinaryReportsLinuxOnlyPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux runs the native resolver")
	}
	_, err := New().ResolveBinary(context.Background())
	if !errors.Is(err, ports.ErrAgentBinaryLinuxOnly) {
		t.Fatalf("ResolveBinary() error = %v, want Linux-only sentinel", err)
	}
}

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
	for _, tt := range []struct{ screen, want string }{{"☤ ❯ msg=interrupt · /queue · Ctrl+C cancel", "active"}, {"────────────────────────────────────────\n☤ ❯ \n────────────────────────────────────────", "idle"}, {"☤ ❯ \nAllow this command?\n❯ [1] Allow once", "blocked"}, {"unrelated text", ""}} {
		state, ok := New().DetectTerminalActivity(tt.screen)
		if string(state) != tt.want || ok != (tt.want != "") {
			t.Fatalf("%q got %s/%v", tt.screen, state, ok)
		}
	}
}

func TestNativeChromeCannotBeForgedInTranscript(t *testing.T) {
	idle := "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
	for _, transcript := range []string{"Dangerous Command", "Allow once", "❯ \n"} {
		state, ok := New().DetectTerminalActivity(transcript + "\n" + idle)
		if !ok || state != "idle" {
			t.Fatalf("quoted transcript %q changed current composer: %s/%v", transcript, state, ok)
		}
	}
	if err := New().ValidateTerminalMessage("task", "❯ "); err == nil {
		t.Fatal("unbordered scrollback prompt admitted input")
	}
}

func TestNativeVersionRequiresExactFirstLine(t *testing.T) {
	for _, output := range []string{"Hermes Agent v0.21.60", "Hermes Agent v0.21.6-malicious", "wrapper\nHermes Agent v0.21.6"} {
		first, _, _ := strings.Cut(output, "\n")
		if nativeVersionLine.MatchString(first) {
			t.Fatalf("accepted version %q", output)
		}
	}
}

func TestNativeStateDSNEscapesQueryCharacters(t *testing.T) {
	dsn := nativeStateDSN("/tmp/hermes?override#fragment")
	if !strings.Contains(dsn, "%3Foverride%23fragment") || !strings.Contains(dsn, "?mode=ro&_pragma=busy_timeout(1000)") {
		t.Fatalf("unsafe native state DSN: %q", dsn)
	}
}

func TestStandingInstructionsIgnoreDaemonEphemeralPrompt(t *testing.T) {
	t.Setenv("HERMES_EPHEMERAL_SYSTEM_PROMPT", "daemon-only")
	home, data := t.TempDir(), t.TempDir()
	env := map[string]string{"HERMES_HOME": home}
	if err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: data, SessionID: "ao-1", Env: env, SystemPrompt: "session"}); err != nil {
		t.Fatal(err)
	}
	if got := env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]; got != "session" {
		t.Fatalf("inherited daemon prompt: %q", got)
	}
}

func TestStandingInstructionsBudgetIncludesEnvironmentKey(t *testing.T) {
	env := map[string]string{"HERMES_HOME": t.TempDir()}
	err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: strings.Repeat("x", 128*1024-len("HERMES_EPHEMERAL_SYSTEM_PROMPT="))})
	if err == nil || !strings.Contains(err.Error(), "process environment limit") {
		t.Fatalf("oversized env entry accepted: %v", err)
	}
}

func TestLeadingDashTaskStaysInPrivateQueryFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Hermes native lease observation supports Linux only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "hermes"), []byte("#!/bin/sh\necho 'Hermes Agent v0.21.6'\n"), 0755); err != nil {
		t.Fatal(err)
	}
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

func TestNativeLookupDoesNotExecuteEarlierPathCandidate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Hermes native lease observation supports Linux only")
	}
	home, pathDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(pathDir, "executed")
	if err := os.WriteFile(filepath.Join(pathDir, "hermes"), []byte("#!/bin/sh\ntouch "+marker+"\necho 'Hermes Agent v0.21.6'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if path, _ := New().ResolveBinary(t.Context()); path == filepath.Join(pathDir, "hermes") {
		t.Fatal("accepted untrusted PATH candidate")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("PATH candidate was executed: %v", err)
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
	if state, _ := New().DetectTerminalActivity("────────────────────────────────────────\n❯ Plan a feature\n────────────────────────────────────────"); state == "idle" {
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

func TestRestoreReportsNativeDatabaseFailureSeparately(t *testing.T) {
	data, home := t.TempDir(), t.TempDir()
	dir := sessionDir(data, "ao-db-error")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(home, "terminal")
	for path, value := range map[string]string{
		filepath.Join(dir, "native-home"):       home,
		filepath.Join(dir, "observed-terminal"): crumb,
		crumb:                                   `{"session_id":"native-id","cwd":"/workspace","ts":1}`,
		filepath.Join(home, "state.db"):         "not a sqlite database",
	} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, ok, err := New().GetRestoreCommand(t.Context(), ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-db-error", WorkspacePath: "/workspace", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-id"}}})
	if ok || err == nil || !strings.Contains(err.Error(), "native state database unavailable") {
		t.Fatalf("database failure disguised as missing history: ok=%v err=%v", ok, err)
	}
}
