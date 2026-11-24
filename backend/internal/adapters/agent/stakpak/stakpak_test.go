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

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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
	// The fake keeps a store per profile; tests choose one explicitly.
	t.Setenv("STAKPAK_PROFILE", "")
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

// AO's update-when-idle check calls GetRestoreCommand without instructions to
// ask whether a live session could resume. That probe must not make the next
// real resume send instructions the conversation already holds.
func TestRestoreProbeWithoutInstructionsKeepsTheirRecord(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, nativeA)
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, SystemPrompt: "rules"}); err != nil {
		t.Fatal(err)
	}
	want := []string{bin, "--session", nativeA}
	restore := ports.RestoreConfig{
		DataDir: data,
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
	}
	if argv, ok, err := p.GetRestoreCommand(ctx, restore); err != nil || !ok || !slices.Equal(argv, want) {
		t.Fatalf("probe = %q, %t, %v; want %q", argv, ok, err, want)
	}
	restore.SystemPrompt = "rules"
	argv, ok, err := p.GetRestoreCommand(ctx, restore)
	if err != nil || !ok || !slices.Equal(argv, want) {
		t.Fatalf("resume after a probe = %q, %t, %v; want %q without the stored instructions", argv, ok, err, want)
	}
}

func TestLaunchRecordMatchesItsOwnConversation(t *testing.T) {
	const other = "55555555-5555-4555-8555-555555555555"
	if !(launchRecord{}).matches(nativeA) {
		t.Fatal("an unresolved fresh launch may be any conversation")
	}
	if r := (launchRecord{ResolvedID: nativeA}); !r.matches(nativeA) || r.matches(other) {
		t.Fatalf("resolved launch %+v must match only %s", r, nativeA)
	}
	if r := (launchRecord{NativeID: nativeA, ResolvedID: other}); !r.matches(nativeA) || r.matches(other) {
		t.Fatalf("resumed launch %+v must match only the conversation it resumed", r)
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
	// An empty or deleted STAKPAK_PROFILE fails config loading with a "not
	// found" message of its own; the conversation may well still exist.
	if _, err := sessionShowResult(nativeA, "Error: Profile '' not found in configuration\n", errors.New("exit 1")); err == nil {
		t.Fatal("a profile Stakpak cannot load must not read as a missing conversation")
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

// A policy AO writes must never be left without its ownership marker: a later
// default-mode launch would keep it as the user's and stay permissive.
func TestPermissionPolicyNeedsItsMarker(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".stakpak", "session")
	// A directory where the marker goes makes its write fail.
	if err := os.MkdirAll(filepath.Join(dir, policyMarker, "blocker"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := applyPermissionMode(ws, ports.PermissionModeBypassPermissions); err == nil {
		t.Fatal("applyPermissionMode succeeded without writing its marker")
	}
	if _, err := os.Stat(filepath.Join(dir, policyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("policy written without an ownership marker: %v", err)
	}
}

// The conversation check runs in the resumed process's environment: a
// conversation in another profile's store exists only for that profile.
func TestRestoreChecksTheConversationInTheRestoreEnvironment(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	t.Setenv("STAKPAK_PROFILE", "")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "known-session-other"), []byte(nativeA), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := ports.RestoreConfig{
		DataDir: t.TempDir(),
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
		Env:     map[string]string{"STAKPAK_PROFILE": "other"},
	}
	argv, ok, err := New().GetRestoreCommand(context.Background(), restore)
	if err != nil || !ok || !slices.Equal(argv, []string{bin, "--session", nativeA}) {
		t.Fatalf("restore in the other profile = %q, %t, %v; want a resume of %s", argv, ok, err, nativeA)
	}
}

// Inline instructions win over the file, so Stakpak must receive the inline
// text whose digest AO records, not a file that says something else.
func TestLaunchPassesTheSelectedInstructions(t *testing.T) {
	fakeStakpak(t, `{"sessions":[]}`, "")
	data := t.TempDir()
	stale := filepath.Join(t.TempDir(), "rules.md")
	if err := os.WriteFile(stale, []byte("older rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir: data, SessionID: "ao-1", WorkspacePath: t.TempDir(), SystemPrompt: "current rules", SystemPromptFile: stale,
	})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(argv, "--system-prompt-file")
	if i < 0 || i+1 >= len(argv) || argv[i+1] == stale {
		t.Fatalf("argv = %q; want AO's copy of the inline instructions, not %s", argv, stale)
	}
	if b, err := os.ReadFile(argv[i+1]); err != nil || string(b) != "current rules" { //nolint:gosec // test path
		t.Fatalf("passed instructions = %q, %v; want the inline text", b, err)
	}

	// A file holding the same text is reused as is.
	same := filepath.Join(t.TempDir(), "same.md")
	if err := os.WriteFile(same, []byte("current rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, err = New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir: data, SessionID: "ao-2", WorkspacePath: t.TempDir(), SystemPrompt: "current rules", SystemPromptFile: same,
	})
	if err != nil || !slices.Contains(argv, same) {
		t.Fatalf("argv = %q, %v; want the matching file %s", argv, err, same)
	}
}

// A resolver that read the session list while a relaunch replaced the launch
// record must not write the earlier launch's conversation over it.
func TestResolvedIDNeverOverwritesANewerLaunch(t *testing.T) {
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	first := launchRecord{Workspace: ws, LaunchedAt: time.Now().UTC().Add(-time.Minute)}
	if err := p.saveLaunchRecord(data, "ao-1", first); err != nil {
		t.Fatal(err)
	}
	seen, _, err := readLaunchRecord(data, "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	newer := launchRecord{Workspace: ws, LaunchedAt: time.Now().UTC(), PromptDigest: promptDigest("rules")}
	if err := p.saveLaunchRecord(data, "ao-1", newer); err != nil {
		t.Fatal(err)
	}
	if stored, err := p.recordResolvedID(data, "ao-1", seen, nativeA); err != nil || stored {
		t.Fatalf("stale resolve stored = %t, %v; want it dropped", stored, err)
	}
	current, _, err := readLaunchRecord(data, "ao-1")
	if err != nil || current.ResolvedID != "" || !current.LaunchedAt.Equal(newer.LaunchedAt) || current.PromptDigest != newer.PromptDigest {
		t.Fatalf("launch record = %+v, %v; want the newer launch untouched", current, err)
	}
	if stored, err := p.recordResolvedID(data, "ao-1", current, nativeA); err != nil || !stored {
		t.Fatalf("current resolve stored = %t, %v; want it recorded", stored, err)
	}
}

// A fresh launch's conversation lives in the launch profile's store, so its
// discovery lists that profile, never the daemon's. The launch record keeps
// only the profile selector, not the environment, which can hold API keys.
func TestResolveNativeSessionIDListsTheLaunchProfile(t *testing.T) {
	ws := t.TempDir()
	const defaultConversation = "66666666-6666-4666-8666-666666666666"
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	data := t.TempDir()
	p := New()
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir: data, SessionID: "ao-1", WorkspacePath: ws,
		Env: map[string]string{"STAKPAK_PROFILE": "other", "ANTHROPIC_API_KEY": "secret-key"},
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(time.Second)
	write := func(name, id string) {
		t.Helper()
		list, err := json.Marshal(map[string]any{"sessions": []map[string]any{{"id": id, "cwd": ws, "created_at": started}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(bin), name), list, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions.json", defaultConversation)
	write("sessions-other.json", nativeA)
	id, ok, err := p.ResolveNativeSessionID(context.Background(), ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-1"})
	if err != nil || !ok || id != nativeA {
		t.Fatalf("resolved = %q, %t, %v; want the other profile's %s", id, ok, err, nativeA)
	}
	raw, err := os.ReadFile(launchRecordPath(data, "ao-1")) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-key") || strings.Contains(string(raw), "ANTHROPIC_API_KEY") {
		t.Fatalf("launch record kept the launch environment: %s", raw)
	}
}

// A profile set only in the project environment reaches the launch, not the
// daemon. AO's update-when-idle probe calls GetRestoreCommand without an
// environment, so the check falls back to the profile the launch recorded,
// and the probe keeps it on the record.
func TestRestoreProbeUsesTheRecordedProfile(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "known-session-other"), []byte(nativeA), 0o600); err != nil {
		t.Fatal(err)
	}
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	if _, err := launchLikeAO(ctx, p, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, Env: map[string]string{"STAKPAK_PROFILE": "other"}}); err != nil {
		t.Fatal(err)
	}
	probe := ports.RestoreConfig{
		DataDir: data,
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
	}
	if argv, ok, err := p.GetRestoreCommand(ctx, probe); err != nil || !ok || !slices.Equal(argv, []string{bin, "--profile", "other", "--session", nativeA}) {
		t.Fatalf("probe = %q, %t, %v; want the other profile's conversation resumed in that profile", argv, ok, err)
	}
	record, _, err := readLaunchRecord(data, "ao-1")
	if err != nil || record.Profile == nil || *record.Profile != "other" {
		t.Fatalf("launch record = %+v, %v; want profile other kept", record, err)
	}
	// An explicit restore environment still wins.
	probe.Env = map[string]string{"STAKPAK_PROFILE": ""}
	if _, ok, err := p.GetRestoreCommand(ctx, probe); err != nil || ok {
		t.Fatalf("restore in the default profile = %t, %v; want the conversation missing there", ok, err)
	}
}

// AO's restore (session_manager restoreArgv) calls GetRestoreCommand with the
// restore environment, then asks NativeConversationExists again with a
// SessionRef that has no data dir and that same environment. When the project
// no longer sets the launch's STAKPAK_PROFILE, both must still find the
// conversation in the launch's store, and the resumed process must open it.
func TestRestoreKeepsTheLaunchProfileWhenTheRestoreEnvironmentDropsIt(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "known-session-other"), []byte(nativeA), 0o600); err != nil {
		t.Fatal(err)
	}
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	if _, err := launchLikeAO(ctx, p, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, Env: map[string]string{"STAKPAK_PROFILE": "other"}}); err != nil {
		t.Fatal(err)
	}
	restoreEnvironment := map[string]string{"AO_SESSION_ID": "ao-1"}
	ref := ports.SessionRef{ID: "ao-1", WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}}
	argv, ok, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{DataDir: data, Session: ref, Env: restoreEnvironment})
	if err != nil || !ok || !slices.Equal(argv, []string{bin, "--profile", "other", "--session", nativeA}) {
		t.Fatalf("restore = %q, %t, %v; want a resume pinned to the launch profile", argv, ok, err)
	}
	// restoreArgv's second check: no data dir on the ref, the same environment.
	if exists, err := p.NativeConversationExists(ctx, ports.SessionRef{ID: "ao-1", WorkspacePath: ws}, nativeA, restoreEnvironment); err != nil || !exists {
		t.Fatalf("restoreArgv conversation check = %t, %v; want it found in the launch profile", exists, err)
	}
	// A ref that carries the data dir works on a fresh plugin too.
	if exists, err := New().NativeConversationExists(ctx, ports.SessionRef{ID: "ao-1", DataDir: data}, nativeA, restoreEnvironment); err != nil || !exists {
		t.Fatalf("conversation check with the ref's data dir = %t, %v; want it found", exists, err)
	}
	// An environment that names a profile is used as is.
	if exists, err := p.NativeConversationExists(ctx, ports.SessionRef{ID: "ao-1"}, nativeA, map[string]string{"STAKPAK_PROFILE": ""}); err != nil || exists {
		t.Fatalf("explicit default profile = %t, %v; want the conversation missing there", exists, err)
	}
}

// A remote profile reads sessions from the Stakpak API, and its
// STAKPAK_API_KEY may be set only in the project environment. Every lookup AO
// makes without an environment (the activity observer's discovery, the
// update-when-idle probe) must use the session's own launch environment, held
// in memory; a restore after a daemon restart uses the restore environment.
// The key is never written to the launch record.
func TestProjectOnlyRemoteCredentialsReachEveryLookup(t *testing.T) {
	const key = "remote-secret-key"
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	t.Setenv("STAKPAK_API_KEY", "")
	dir := filepath.Dir(bin)
	data, ws := t.TempDir(), t.TempDir()
	launchEnv := map[string]string{"STAKPAK_PROFILE": "remote", "STAKPAK_API_KEY": key}
	p := New()
	ctx := context.Background()
	sessions := []string{"ao-1", "ao-2", "ao-3", "ao-4"}
	for _, id := range sessions {
		if _, err := launchLikeAO(ctx, p, ports.LaunchConfig{DataDir: data, SessionID: id, WorkspacePath: ws, Env: launchEnv}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := json.Marshal(map[string]any{"sessions": []map[string]any{{"id": nativeA, "cwd": ws, "created_at": time.Now().UTC().Add(time.Second)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions-remote.json"), list, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known-session-remote"), []byte(nativeA), 0o600); err != nil {
		t.Fatal(err)
	}

	// The observer's discovery passes no environment.
	if id, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-1"}); err != nil || !ok || id != nativeA {
		t.Fatalf("observer discovery = %q, %t, %v; want %s found with the launch credentials", id, ok, err, nativeA)
	}
	// So does the update-when-idle probe.
	probe := ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-1", WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}}}
	if _, ok, err := p.GetRestoreCommand(ctx, probe); err != nil || !ok {
		t.Fatalf("update-when-idle probe = %t, %v; want the conversation found", ok, err)
	}
	// After a daemon restart nothing is in memory: a restore that must still
	// discover its id lists with the restore environment.
	restarted := New()
	restore := ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-2", WorkspacePath: ws, Metadata: map[string]string{}}, Env: launchEnv}
	argv, ok, err := restarted.GetRestoreCommand(ctx, restore)
	if err != nil || !ok || !slices.Equal(argv, []string{bin, "--session", nativeA}) {
		t.Fatalf("restore after a restart = %q, %t, %v; want a resume of %s", argv, ok, err, nativeA)
	}
	// AO's update-when-idle probe passes the session environment too, so after
	// a restart it can still discover an id the observer never captured.
	afterRestartProbe := ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-3", WorkspacePath: ws, Metadata: map[string]string{}}, Env: launchEnv}
	if _, ok, err := restarted.GetRestoreCommand(ctx, afterRestartProbe); err != nil || !ok {
		t.Fatalf("update-when-idle probe after a restart = %t, %v; want the conversation found", ok, err)
	}
	// The activity observer passes the session's runtime environment, so its
	// discovery works after a restart without anything in memory.
	if id, ok, err := restarted.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-4", Env: launchEnv}); err != nil || !ok || id != nativeA {
		t.Fatalf("observer discovery after a restart = %q, %t, %v; want %s", id, ok, err, nativeA)
	}
	for _, id := range sessions {
		raw, err := os.ReadFile(launchRecordPath(data, id)) //nolint:gosec // test path
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), key) || strings.Contains(string(raw), "STAKPAK_API_KEY") {
			t.Fatalf("launch record %s kept the credential: %s", id, raw)
		}
	}
}

// An account move's preflight checks the conversation with the target
// account's environment while the session still runs on its own. That check
// must not replace the session's kept environment: a later lookup that passes
// none would otherwise run with the target's credentials and profile.
func TestConversationChecksNeverReplaceTheSessionEnvironment(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	t.Setenv("STAKPAK_API_KEY", "")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "known-session-remote"), []byte(nativeA), 0o600); err != nil {
		t.Fatal(err)
	}
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	own := map[string]string{"STAKPAK_PROFILE": "remote", "STAKPAK_API_KEY": "own-key"}
	if _, err := launchLikeAO(ctx, p, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, Env: own}); err != nil {
		t.Fatal(err)
	}
	target := map[string]string{"STAKPAK_PROFILE": "elsewhere", "STAKPAK_API_KEY": "target-account-key"}
	if exists, err := p.NativeConversationExists(ctx, ports.SessionRef{ID: "ao-1", WorkspacePath: ws}, nativeA, target); err != nil || exists {
		t.Fatalf("preflight check in the target environment = %t, %v; want the conversation missing there", exists, err)
	}
	p.recordMu.Lock()
	kept := p.launchEnvs["ao-1"]
	p.recordMu.Unlock()
	if kept["STAKPAK_API_KEY"] != "own-key" || kept["STAKPAK_PROFILE"] != "remote" {
		t.Fatalf("kept environment = %v; want the session's own, not the preflight's", kept)
	}
	// A lookup that passes no environment still uses the session's own.
	if exists, err := p.NativeConversationExists(ctx, ports.SessionRef{ID: "ao-1", WorkspacePath: ws}, nativeA, nil); err != nil || !exists {
		t.Fatalf("check without an environment after a preflight = %t, %v; want the session's own store", exists, err)
	}
}

// The activity observer passes an environment AO rebuilds from the current
// project and account. After a key change it names credentials the running
// Stakpak never received, so discovery uses the launch environment this
// process kept, and the rebuilt one only when nothing is kept.
func TestResolveNativeSessionIDPrefersTheRunningLaunchEnvironment(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	t.Setenv("STAKPAK_API_KEY", "")
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	launched := map[string]string{"STAKPAK_PROFILE": "remote", "STAKPAK_API_KEY": "launch-key"}
	if _, err := launchLikeAO(ctx, p, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, Env: launched}); err != nil {
		t.Fatal(err)
	}
	list, err := json.Marshal(map[string]any{"sessions": []map[string]any{{"id": nativeA, "cwd": ws, "created_at": time.Now().UTC().Add(time.Second)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "sessions-remote.json"), list, 0o600); err != nil {
		t.Fatal(err)
	}
	rebuilt := map[string]string{"STAKPAK_PROFILE": "remote", "STAKPAK_API_KEY": "revoked"}
	if id, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-1", Env: rebuilt}); err != nil || !ok || id != nativeA {
		t.Fatalf("discovery with a rebuilt environment = %q, %t, %v; want %s via the running launch's credentials", id, ok, err, nativeA)
	}
}

// launchLikeAO starts a session the way the session manager does: the launch
// command, then PreLaunch with the environment the process runs with.
func launchLikeAO(ctx context.Context, p *Plugin, cfg ports.LaunchConfig) ([]string, error) {
	argv, err := p.GetLaunchCommand(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return argv, p.PreLaunch(ctx, cfg)
}

// AO's update-when-idle probe builds a restore command with an environment
// rebuilt from the current project and account, and starts nothing. Only
// PreLaunch, which runs for a process AO actually starts, keeps the session's
// environment, so the probe leaves the running launch's own in place.
func TestRestoreProbeLeavesTheKeptLaunchEnvironment(t *testing.T) {
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	t.Setenv("STAKPAK_API_KEY", "")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "known-session-remote"), []byte(nativeA), 0o600); err != nil {
		t.Fatal(err)
	}
	data, ws := t.TempDir(), t.TempDir()
	p := New()
	ctx := context.Background()
	launched := map[string]string{"STAKPAK_PROFILE": "remote", "STAKPAK_API_KEY": "launch-key"}
	if _, err := launchLikeAO(ctx, p, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, Env: launched}); err != nil {
		t.Fatal(err)
	}
	rebuilt := map[string]string{"STAKPAK_PROFILE": "remote", "STAKPAK_API_KEY": "rotated-key"}
	probe := ports.RestoreConfig{
		DataDir: data, Env: rebuilt,
		Session: ports.SessionRef{ID: "ao-1", WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeA}},
	}
	if _, ok, err := p.GetRestoreCommand(ctx, probe); err != nil || !ok {
		t.Fatalf("probe = %t, %v; want the conversation found", ok, err)
	}
	if kept := p.keptEnv("ao-1"); kept["STAKPAK_API_KEY"] != "launch-key" {
		t.Fatalf("kept environment after a probe = %v; want the running launch's", kept)
	}
	// A real restore starts a process: its PreLaunch keeps that environment.
	if err := p.PreLaunch(ctx, ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: ws, Env: rebuilt}); err != nil {
		t.Fatal(err)
	}
	if kept := p.keptEnv("ao-1"); kept["STAKPAK_API_KEY"] != "rotated-key" {
		t.Fatalf("kept environment after a restore = %v; want the resumed process's", kept)
	}
}
