package stakpak

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeStakpak puts a `stakpak` script on PATH that answers --version and the
// session commands the adapter runs, and returns the script's path.
func fakeStakpak(t *testing.T, sessionsJSON string, knownSession string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a POSIX shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(sessionsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
--version) echo "stakpak 0.3.88" ;;
sessions)
  case "$2" in
  list) cat "` + filepath.Join(dir, "sessions.json") + `" ;;
  show)
    if [ "$3" = "` + knownSession + `" ]; then echo "ID:          $3"; else echo "Error: Not found: Session $3 not found" >&2; exit 1; fi ;;
  esac ;;
esac
`
	path := filepath.Join(dir, "stakpak")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test-only executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	return path
}

func TestParseVersion(t *testing.T) {
	for out, want := range map[string]string{
		"stakpak 0.3.88\n":    "0.3.88",
		"stakpak":             "",
		"other 0.3.88":        "",
		"stakpak 0.3.88 beta": "",
	} {
		if got := parseVersion(out); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestLaunchCommandPassesSystemPromptFileAndModel(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	data := t.TempDir()
	argv, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir: data, SessionID: "ao-1", WorkspacePath: t.TempDir(),
		SystemPrompt: "You are an AO worker.", Prompt: "do the task",
		Config: ports.AgentConfig{Model: "anthropic/claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	promptFile := filepath.Join(sessionDir(data, "ao-1"), "system-prompt.md")
	want := []string{bin, "--system-prompt-file", promptFile, "--model", "anthropic/claude-sonnet-4-5"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	if slices.Contains(argv, "do the task") {
		t.Fatal("the TUI ignores a positional prompt; the task must be delivered after start")
	}
	if b, err := os.ReadFile(promptFile); err != nil || string(b) != "You are an AO worker." { //nolint:gosec // test path
		t.Fatalf("system prompt file = %q, %v", b, err)
	}
	record, ok, err := readLaunchRecord(data, "ao-1")
	if err != nil || !ok || record.PromptDigest != promptDigest("You are an AO worker.") {
		t.Fatalf("launch record = %+v, %t, %v", record, ok, err)
	}
}

func TestLaunchCommandRejectsUnsupportedSettings(t *testing.T) {
	fakeStakpak(t, `{"sessions":[]}`, "")
	p := New()
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{Effort: "high"}}); err == nil {
		t.Fatal("effort override accepted")
	}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{AllowedTools: []string{"Read"}}); err == nil {
		t.Fatal("tool restriction accepted")
	}
}

func TestPromptDeliveryIsAfterStart(t *testing.T) {
	got, err := New().GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil || got != ports.PromptDeliveryAfterStart {
		t.Fatalf("strategy = %q, %v", got, err)
	}
}

const nativeA = "23491b8c-8861-4d57-aa2d-37193c68ff6a"

func TestRestoreResumesByIDWithoutRepeatingInstructions(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, nativeA)
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, SystemPrompt: "rules"}); err != nil {
		t.Fatal(err)
	}
	restore := ports.RestoreConfig{
		DataDir: data, SystemPrompt: "rules",
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
	}
	argv, ok, err := p.GetRestoreCommand(ctx, restore)
	if err != nil || !ok {
		t.Fatalf("restore = %q, %t, %v", argv, ok, err)
	}
	// The conversation already holds these instructions from its first launch.
	if want := []string{bin, "--session", nativeA}; !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	restore.SystemPrompt = "new rules"
	argv, ok, err = p.GetRestoreCommand(ctx, restore)
	if err != nil || !ok || !slices.Contains(argv, "--system-prompt-file") {
		t.Fatalf("changed instructions not passed on restore: %q, %t, %v", argv, ok, err)
	}
}

func TestRestoreFallsBackWhenStakpakForgotTheConversation(t *testing.T) {
	fakeStakpak(t, `{"sessions":[]}`, "")
	argv, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(),
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
	})
	if err != nil || ok || argv != nil {
		t.Fatalf("restore of a missing conversation = %q, %t, %v; want fresh-launch fallback", argv, ok, err)
	}
}

func TestRestoreRejectsMalformedNativeID(t *testing.T) {
	fakeStakpak(t, `{"sessions":[]}`, "")
	_, _, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(),
		Session: ports.SessionRef{ID: "ao-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "--help"}},
	})
	if err == nil {
		t.Fatal("malformed native id accepted")
	}
}

func TestResolveNativeSessionIDMatchesWorktreeAndLaunchTime(t *testing.T) {
	ws := t.TempDir()
	launched := time.Now().UTC().Add(-time.Minute)
	sessions := []map[string]any{
		{"id": "11111111-1111-4111-8111-111111111111", "cwd": ws, "created_at": launched.Add(-time.Hour)},
		{"id": nativeA, "cwd": ws, "created_at": launched.Add(10 * time.Second)},
		{"id": "22222222-2222-4222-8222-222222222222", "cwd": t.TempDir(), "created_at": launched.Add(20 * time.Second)},
	}
	list, err := json.Marshal(map[string]any{"sessions": sessions})
	if err != nil {
		t.Fatal(err)
	}
	fakeStakpak(t, string(list), "")
	data := t.TempDir()
	if err := writeLaunchRecord(data, "ao-1", launchRecord{Workspace: ws, LaunchedAt: launched}); err != nil {
		t.Fatal(err)
	}
	id, ok, err := New().ResolveNativeSessionID(context.Background(), ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-1"})
	if err != nil || !ok || id != nativeA {
		t.Fatalf("resolved = %q, %t, %v; want %s", id, ok, err, nativeA)
	}
	if _, ok, err := New().ResolveNativeSessionID(context.Background(), ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-2"}); ok || err != nil {
		t.Fatalf("unknown launch resolved: %t, %v", ok, err)
	}
}

func TestMatchLaunchPrefersNewConversationOverResumedOne(t *testing.T) {
	ws := t.TempDir()
	launched := time.Now().UTC()
	record := launchRecord{Workspace: ws, LaunchedAt: launched, NativeID: nativeA}
	if id, ok := matchLaunch(record, nil); !ok || id != nativeA {
		t.Fatalf("resumed id = %q, %t", id, ok)
	}
	fresh := "33333333-3333-4333-8333-333333333333"
	id, ok := matchLaunch(record, []nativeSession{{ID: fresh, Cwd: ws, CreatedAt: launched.Add(time.Second)}})
	if !ok || id != fresh {
		t.Fatalf("id after /new = %q, %t; want %s", id, ok, fresh)
	}
}

func TestSessionShowResult(t *testing.T) {
	if ok, err := sessionShowResult(nativeA, "Backend: local\nID:          "+nativeA+"\n", nil); !ok || err != nil {
		t.Fatalf("found = %t, %v", ok, err)
	}
	if ok, err := sessionShowResult(nativeA, "Error: Not found: Session "+nativeA+" not found\n", errors.New("exit 1")); ok || err != nil {
		t.Fatalf("missing = %t, %v", ok, err)
	}
	if _, err := sessionShowResult(nativeA, "Failed to load config", errors.New("exit 1")); err == nil {
		t.Fatal("an unrelated failure must not read as a missing conversation")
	}
}

func TestPermissionPolicyFile(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, ".stakpak", "session", "auto_approve.json")
	if err := applyPermissionMode(ws, ports.PermissionModeAcceptEdits); err != nil {
		t.Fatal(err)
	}
	var config autoApproveConfig
	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.DefaultPolicy != policyPrompt || config.Tools["create"] != policyAuto || config.Tools["run_command"] != "" {
		t.Fatalf("accept-edits policy = %+v", config)
	}
	if b, err := os.ReadFile(filepath.Join(ws, ".stakpak", "session", ".gitignore")); err != nil || string(b) != policyIgnored { //nolint:gosec // test path
		t.Fatalf("session footprint is not git-ignored: %q, %v", b, err)
	}
	if err := applyPermissionMode(ws, ports.PermissionModeBypassPermissions); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path) //nolint:gosec // test path
	if !strings.Contains(string(data), `"default_policy": "Auto"`) {
		t.Fatalf("bypass policy = %s", data)
	}
	// A later manual launch must not keep AO's more permissive policy.
	if err := applyPermissionMode(ws, ports.PermissionModeDefault); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("AO policy survived a manual launch: %v", err)
	}
}

func TestPermissionPolicyLeavesUserFileAlone(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".stakpak", "session")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auto_approve.json")
	const own = `{"enabled":false}`
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeBypassPermissions, ports.PermissionModeDefault} {
		if err := applyPermissionMode(ws, mode); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != own { //nolint:gosec // test path
			t.Fatalf("%s overwrote the user's policy: %s", mode, b)
		}
	}
}
