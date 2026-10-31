package jcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const testSessionID = "session_crab_1793367474433_0a651258c3f6076a"

func testPlugin(t *testing.T) *Plugin {
	t.Helper()
	return &Plugin{resolvedBinary: "/opt/jcode/bin/jcode"}
}

// shortDir is a temp dir short enough for the daemon's socket paths.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "jc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeNativeSession(t *testing.T, home, id, workingDir string) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + id + `","title":"say hi","working_dir":` + quoteJSON(workingDir) + `,"messages":[]}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func TestLaunchCommandDisablesSelfUpdateAndPassesModel(t *testing.T) {
	cmd, err := testPlugin(t).GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{Model: "claude-opus-5-5"},
		Prompt: "--do not become a flag",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/jcode/bin/jcode", "--no-update", "--model", "claude-opus-5-5"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("argv = %q, want %q (the prompt is delivered after start, never in argv)", cmd, want)
	}
}

func TestLaunchRejectsUnsupportedTuningInsteadOfIgnoringIt(t *testing.T) {
	cases := map[string]ports.LaunchConfig{
		"effort":    {Config: ports.AgentConfig{Effort: "high"}},
		"mode":      {Config: ports.AgentConfig{Mode: "plan"}},
		"allowlist": {AllowedTools: []string{"Read"}},
		"denylist":  {DisallowedTools: []string{"Bash"}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := testPlugin(t).GetLaunchCommand(context.Background(), cfg); err == nil {
				t.Fatal("unsupported JCode tuning was accepted")
			}
		})
	}
	_, err := testPlugin(t).GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{Effort: "high"}})
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("effort error = %v, want ErrUnsupportedEffort", err)
	}
}

func TestPromptIsDeliveredAfterStartOnceTheComposerRenders(t *testing.T) {
	p := testPlugin(t)
	strategy, err := p.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{Prompt: "x"})
	if err != nil || strategy != ports.PromptDeliveryAfterStart {
		t.Fatalf("strategy = %q, %v; want after_start", strategy, err)
	}
	hints, err := p.PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	onboarding := "                 Welcome to jcode onboarding\n                 Log in to OpenAI?\n                 ◖ Yes ◗   ◖ No ◗\n"
	composer := "Onboarding skipped - run /login when you're ready\n1>\n        /tmp/ws 3.1k/1.0M ▱▱▱▱▱▱▱▱▱▱ 0% Claude Opus 5.5\n"
	if matchesAny(onboarding, hints.Patterns) {
		t.Fatalf("readiness matched the onboarding page: %q", hints.Patterns)
	}
	if !matchesAny(composer, hints.Patterns) {
		t.Fatalf("readiness did not match the empty composer: %q", hints.Patterns)
	}
	if hints.Timeout <= 0 {
		t.Fatal("readiness has no bounded wait")
	}
}

func matchesAny(output string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

func TestAfterStartPromptUsesBracketedPasteForMultilineAndLongPrompts(t *testing.T) {
	p := testPlugin(t)
	single, err := p.BuildAfterStartPrompt(context.Background(), ports.LaunchConfig{Prompt: "fix the bug"})
	if err != nil || single != "fix the bug" {
		t.Fatalf("single-line prompt = %q, %v", single, err)
	}
	multi, err := p.BuildAfterStartPrompt(context.Background(), ports.LaunchConfig{Prompt: "line one\r\nline two\x1b[201~ injected\nline three"})
	if err != nil {
		t.Fatal(err)
	}
	want := "\x1b[200~line one\nline two[201~ injected\nline three\x1b[201~"
	if multi != want {
		t.Fatalf("multi-line prompt = %q, want %q", multi, want)
	}
	long := strings.Repeat("x", 9000)
	wrapped, err := p.BuildAfterStartPrompt(context.Background(), ports.LaunchConfig{Prompt: long})
	if err != nil {
		t.Fatal(err)
	}
	if wrapped != "\x1b[200~"+long+"\x1b[201~" {
		t.Fatalf("a >8 KiB prompt must be pasted as one block; got %d bytes", len(wrapped))
	}
}

func TestRestoreResumesOnlyTheExactSessionOfThisWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JCODE_HOME", home)
	workspace := t.TempDir()
	writeNativeSession(t, home, testSessionID, workspace)
	p := testPlugin(t)
	cfg := ports.RestoreConfig{
		Config:  ports.AgentConfig{Model: "claude-sonnet-5"},
		Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: testSessionID}},
	}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok {
		t.Fatalf("restore = %q, %v, %v", cmd, ok, err)
	}
	want := []string{"/opt/jcode/bin/jcode", "--no-update", "--model", "claude-sonnet-5", "--resume", testSessionID}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("argv = %q, want %q", cmd, want)
	}
	exists, err := p.NativeConversationExists(context.Background(), cfg.Session, testSessionID, nil)
	if err != nil || !exists {
		t.Fatalf("NativeConversationExists = %v, %v", exists, err)
	}
}

func TestRestoreFallsBackFreshForMissingForeignOrAmbiguousConversations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JCODE_HOME", home)
	workspace := t.TempDir()
	foreign := t.TempDir()
	foreignID := "session_fox_1793367230599_7049d57744447aef"
	writeNativeSession(t, home, foreignID, foreign)
	cases := map[string]string{
		"none":    "",
		"missing": "session_owl_1793367230599_0000000000000000",
		"foreign": foreignID,
		// `jcode --resume` also matches short names and titles; only exact ids resume.
		"short-name": "fox",
		"title":      "say hi",
		"flag":       "--help",
	}
	p := testPlugin(t)
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			ref := ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}
			cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ref})
			if err != nil || ok {
				t.Fatalf("restore = %q, %v, %v; want a fresh-launch fallback", cmd, ok, err)
			}
			exists, err := p.NativeConversationExists(context.Background(), ref, id, nil)
			if err != nil || exists {
				t.Fatalf("NativeConversationExists = %v, %v; want false", exists, err)
			}
		})
	}
}

func TestSessionInfoReadsNativeTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JCODE_HOME", home)
	workspace := t.TempDir()
	writeNativeSession(t, home, testSessionID, workspace)
	info, ok, err := testPlugin(t).SessionInfo(context.Background(), ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: testSessionID}})
	if err != nil || !ok || info.AgentSessionID != testSessionID || info.Title != "say hi" {
		t.Fatalf("SessionInfo = %+v, %v, %v", info, ok, err)
	}
}

func TestGetAgentHooksInstallsHiddenInstructionsAsIgnoredOverlay(t *testing.T) {
	workspace := t.TempDir()
	dataDir := shortDir(t)
	p := testPlugin(t)
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, DataDir: dataDir, SessionID: "proj-7", SystemPrompt: "You are an AO worker."}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	overlay, err := os.ReadFile(filepath.Join(workspace, ".jcode", "prompt-overlay.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(overlay), "You are an AO worker.") || !strings.Contains(string(overlay), overlaySentinel) {
		t.Fatalf("overlay = %q", overlay)
	}
	ignore, err := os.ReadFile(filepath.Join(workspace, ".jcode", ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), "prompt-overlay.md") {
		t.Fatalf("overlay is not git-ignored: %q, %v", ignore, err)
	}
	if info, err := os.Stat(runtimeDir(dataDir, "proj-7")); err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime dir = %v, %v", info, err)
	}

	promptFile := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(promptFile, []byte("Orchestrator role from file."), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.SystemPrompt, cfg.SystemPromptFile = "", promptFile
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	overlay, _ = os.ReadFile(filepath.Join(workspace, ".jcode", "prompt-overlay.md"))
	if !strings.Contains(string(overlay), "Orchestrator role from file.") || strings.Contains(string(overlay), "AO worker") {
		t.Fatalf("overlay not refreshed from file: %q", overlay)
	}

	cfg.SystemPromptFile = ""
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".jcode", "prompt-overlay.md")); !os.IsNotExist(err) {
		t.Fatalf("stale AO overlay kept without instructions: %v", err)
	}
}

func TestGetAgentHooksRefusesToReplaceUserOverlay(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".jcode", "prompt-overlay.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("project rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := testPlugin(t).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, DataDir: shortDir(t), SessionID: "proj-1", SystemPrompt: "AO"})
	if err == nil {
		t.Fatal("user overlay was overwritten")
	}
	if data, _ := os.ReadFile(path); string(data) != "project rules" {
		t.Fatalf("user overlay changed: %q", data)
	}
}

func TestRuntimeLaunchEnvIsolatesDaemonAndKeepsUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JCODE_HOME", home)
	t.Setenv("JCODE_HOOK_SESSION_START", "")
	os.Unsetenv("JCODE_HOOK_SESSION_START")
	t.Setenv("JCODE_HOOK_TURN_START", `["notify-send start", "ao hooks jcode user-prompt-submit"]`)
	config := "[hooks]\nturn_end = \"~/bin/done --loud\"\nsession_end = [\"a\", \"b c\"]\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	env := map[string]string{}
	testPlugin(t).AugmentRuntimeLaunchEnv(env, dataDir, domain.SessionID("proj-7"), "launch-1")

	if env["JCODE_RUNTIME_DIR"] != runtimeDir(dataDir, "proj-7") {
		t.Fatalf("JCODE_RUNTIME_DIR = %q", env["JCODE_RUNTIME_DIR"])
	}
	if env["JCODE_NO_AUTO_UPDATE"] != "1" || env["JCODE_CHECK_UPDATES"] != "0" {
		t.Fatalf("self-update not disabled: %v", env)
	}
	want := map[string][]string{
		"JCODE_HOOK_SESSION_START": {"ao hooks jcode session-start"},
		"JCODE_HOOK_TURN_START":    {"notify-send start", "ao hooks jcode user-prompt-submit"},
		"JCODE_HOOK_TURN_END":      {"~/bin/done --loud", "ao hooks jcode stop"},
		"JCODE_HOOK_SESSION_END":   {"a", "b c", "ao hooks jcode session-end"},
	}
	for key, commands := range want {
		var got struct {
			Commands []string `toml:"commands"`
		}
		if err := toml.Unmarshal([]byte("commands = "+env[key]), &got); err != nil {
			t.Fatalf("%s = %q is not a native TOML hook array: %v", key, env[key], err)
		}
		if !reflect.DeepEqual(got.Commands, commands) {
			t.Fatalf("%s = %q, want %q", key, got.Commands, commands)
		}
	}
}

// A long AO data dir (deep home, custom AO_DATA_DIR) cannot hold the daemon
// socket within sun_path; the session falls back to a short private directory
// in the OS temp dir, the same one for launch, hooks and Kill.
func TestRuntimeDirFallsBackWhenTheDataDirIsTooLongForASocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket limit")
	}
	tmp, err := os.MkdirTemp("/tmp", "jc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	dir := runtimeDir(long, "proj-1")
	if len(filepath.Join(dir, longestSocket)) > maxSocketPath || strings.HasPrefix(dir, long) {
		t.Fatalf("runtime dir = %q", dir)
	}
	if err := testPlugin(t).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: t.TempDir(), DataDir: long, SessionID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("fallback dir = %v, %v", info, err)
	}
	env := map[string]string{}
	testPlugin(t).AugmentRuntimeLaunchEnv(env, long, domain.SessionID("proj-1"), "launch-1")
	if env["JCODE_RUNTIME_DIR"] != dir || env["JCODE_SOCKET"] != filepath.Join(dir, "jcode.sock") {
		t.Fatalf("launch env = %v, want %s", env, dir)
	}
	// A pre-made path someone else controls is refused rather than reused.
	other := runtimeDir(long, "proj-2")
	if err := os.Symlink(t.TempDir(), other); err != nil {
		t.Fatal(err)
	}
	if err := testPlugin(t).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: t.TempDir(), DataDir: long, SessionID: "proj-2"}); err == nil {
		t.Fatal("a symlinked runtime dir was accepted")
	}
}

func TestDeriveActivityState(t *testing.T) {
	cases := map[string]domain.ActivityState{
		"session-start":      domain.ActivityIdle,
		"user-prompt-submit": domain.ActivityActive,
		"stop":               domain.ActivityIdle,
		"session-end":        domain.ActivityExited,
	}
	for event, want := range cases {
		got, ok := DeriveActivityState(event, nil)
		if !ok || got != want {
			t.Fatalf("%s = %q, %v; want %q", event, got, ok, want)
		}
	}
	if _, ok := DeriveActivityState("permission-request", nil); ok {
		t.Fatal("JCode has no permission hook; the event must not map")
	}
}

func TestSupportsTerminalVersionRefresh(t *testing.T) {
	if !(&Plugin{}).SupportsTerminalVersionRefresh() {
		t.Fatal("exact --resume restore should opt into version refresh")
	}
}

func TestRuntimeLaunchEnvPinsThePrivateSocketOverInheritedAndProjectValues(t *testing.T) {
	t.Setenv("JCODE_HOME", t.TempDir())
	t.Setenv("JCODE_SOCKET", "/tmp/user-shared/jcode.sock")
	dataDir := shortDir(t)
	env := map[string]string{"JCODE_SOCKET": "/srv/project/jcode.sock", "JCODE_RUNTIME_DIR": "/srv/project"}
	testPlugin(t).AugmentRuntimeLaunchEnv(env, dataDir, domain.SessionID("proj-7"), "launch-1")
	dir := runtimeDir(dataDir, "proj-7")
	if env["JCODE_SOCKET"] != filepath.Join(dir, "jcode.sock") || env["JCODE_RUNTIME_DIR"] != dir {
		t.Fatalf("socket env = %q / %q; want the session's private daemon", env["JCODE_SOCKET"], env["JCODE_RUNTIME_DIR"])
	}
}

func TestOverlayRefusesSymlinkedJCodeDirectoryAndUnanchoredMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture")
	}
	outside := t.TempDir()
	workspace := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, ".jcode")); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, DataDir: shortDir(t), SessionID: "proj-1", SystemPrompt: "private AO instructions"}
	if err := testPlugin(t).GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("instructions written through a symlinked .jcode directory")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the workspace: %v", entries)
	}

	workspace = t.TempDir()
	target := filepath.Join(outside, "global-overlay.md")
	if err := os.WriteFile(target, []byte(overlaySentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".jcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, overlayPath(workspace)); err != nil {
		t.Fatal(err)
	}
	cfg.WorkspacePath = workspace
	if err := testPlugin(t).GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("instructions written through a symlinked overlay file")
	}

	workspace = t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".jcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	quoted := "Project notes that mention " + overlaySentinel + " in passing.\n"
	if err := os.WriteFile(overlayPath(workspace), []byte(quoted), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.WorkspacePath = workspace
	if err := testPlugin(t).GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("a user file that only mentions the marker was treated as AO-owned")
	}
}
