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

// fakeStakpak puts a copy of this test binary on PATH under Stakpak's
// executable name; TestMain makes it answer --version and the session
// commands the adapter runs. It returns the fake's path, which binary
// discovery is expected to find.
func fakeStakpak(t *testing.T, sessionsJSON string, knownSession string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(sessionsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known-session"), []byte(knownSession), 0o600); err != nil {
		t.Fatal(err)
	}
	name := binarySpec.Names[0]
	if runtime.GOOS == "windows" {
		name = binarySpec.WinNames[0]
	}
	path := filepath.Join(dir, name)
	// A copy, not a link: Windows cannot delete a link to the running test
	// binary when the temp dir is cleaned up.
	copyTestBinary(t, path)
	t.Setenv(fakeStakpakDirEnv, dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	return path
}

func copyTestBinary(t *testing.T, dst string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe) //nolint:gosec // the running test binary
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o700); err != nil { //nolint:gosec // test-only executable
		t.Fatal(err)
	}
}

// fakeStakpakCalls returns the arguments of every call the fake answered.
func fakeStakpakCalls(t *testing.T, bin string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "calls.log")) //nolint:gosec // test path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// breakFakeStakpak makes every later call to the fake fail.
func breakFakeStakpak(t *testing.T, bin string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "broken"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
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
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	argv, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(),
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
	})
	if err != nil || ok || argv != nil {
		t.Fatalf("restore of a missing conversation = %q, %t, %v; want fresh-launch fallback", argv, ok, err)
	}
	// The fallback must come from Stakpak reporting the conversation missing,
	// not from a restore that never asked.
	calls := fakeStakpakCalls(t, bin)
	if want := "sessions show " + nativeA + " --limit 1"; !slices.Contains(calls, want) {
		t.Fatalf("fake stakpak calls = %q; want the conversation probed with %q", calls, want)
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

func TestMatchLaunchTakesTheNewestConversationSinceLaunch(t *testing.T) {
	ws := t.TempDir()
	launched := time.Now().UTC()
	record := launchRecord{Workspace: ws, LaunchedAt: launched}
	if _, ok := matchLaunch(record, nil); ok {
		t.Fatal("matched a conversation in an empty list")
	}
	older, newer := "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	id, ok := matchLaunch(record, []nativeSession{
		{ID: newer, Cwd: ws, CreatedAt: launched.Add(2 * time.Second)},
		{ID: older, Cwd: ws, CreatedAt: launched.Add(time.Second)},
	})
	if !ok || id != newer {
		t.Fatalf("id = %q, %t; want %s", id, ok, newer)
	}
}

// The observer resolves the id on every activity change; once known it must
// not run the session list (a Stakpak API call on remote profiles) again.
func TestResolveNativeSessionIDListsOnlyUntilKnown(t *testing.T) {
	ws := t.TempDir()
	launched := time.Now().UTC().Add(-time.Minute)
	list, err := json.Marshal(map[string]any{"sessions": []map[string]any{{"id": nativeA, "cwd": ws, "created_at": launched.Add(time.Second)}}})
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeStakpak(t, string(list), "")
	data := t.TempDir()
	if err := writeLaunchRecord(data, "ao-1", launchRecord{Workspace: ws, LaunchedAt: launched}); err != nil {
		t.Fatal(err)
	}
	p := New()
	cfg := ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-1"}
	if id, ok, err := p.ResolveNativeSessionID(context.Background(), cfg); err != nil || !ok || id != nativeA {
		t.Fatalf("first lookup = %q, %t, %v", id, ok, err)
	}
	// Break the CLI: a second lookup that still listed would now fail.
	breakFakeStakpak(t, bin)
	if id, ok, err := p.ResolveNativeSessionID(context.Background(), cfg); err != nil || !ok || id != nativeA {
		t.Fatalf("cached lookup = %q, %t, %v", id, ok, err)
	}
	// A resumed launch knows its conversation without any list.
	if err := writeLaunchRecord(data, "ao-2", launchRecord{Workspace: ws, LaunchedAt: launched, NativeID: nativeA}); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := p.ResolveNativeSessionID(context.Background(), ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-2"}); err != nil || !ok || id != nativeA {
		t.Fatalf("resumed lookup = %q, %t, %v", id, ok, err)
	}
}

func TestSessionShowResult(t *testing.T) {
	if ok, err := sessionShowResult(nativeA, "Backend: local\nID:          "+nativeA+"\n", nil); !ok || err != nil {
		t.Fatalf("found = %t, %v", ok, err)
	}
	if ok, err := sessionShowResult(nativeA, "Error: Not found: Session "+nativeA+" not found\n", errors.New("exit 1")); ok || err != nil {
		t.Fatalf("missing = %t, %v", ok, err)
	}
	// A remote profile reports the Stakpak API's error instead.
	if ok, err := sessionShowResult(nativeA, "Error: API error 404 Not Found: {}\n", errors.New("exit 1")); ok || err != nil {
		t.Fatalf("remote missing = %t, %v", ok, err)
	}
	if ok, err := sessionShowResult(nativeA, "Error: Session not found\n", errors.New("exit 1")); ok || err != nil {
		t.Fatalf("remote not-found message = %t, %v", ok, err)
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
