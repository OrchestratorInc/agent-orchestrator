// Tests for the per-role env-profile surface: the MCP/plugin flags a config
// contributes to launch and restore argv, and the transcript probe that gates
// --resume. They live beside claudecode_profile.go rather than in
// claudecode_test.go for the same reason the code does — upstream rewrites
// that file often, and fork-only tests sitting in it conflict on every rebase.
package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestGetLaunchCommandMCPAndPluginFlags: a per-role MCP set and plugin list are
// emitted as claude-code flags. MCP configs go to --mcp-config (one per entry),
// Strict adds --strict-mcp-config, local plugin paths go to --plugin-dir, and
// http(s) URLs go to --plugin-url.
func TestGetLaunchCommandMCPAndPluginFlags(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{
			MCP:        &domain.MCPConfig{Configs: []string{"{\"a\":1}", "/p/m.json"}, Strict: true},
			PluginDirs: []string{"/local/plugin", "https://example.com/p.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"--mcp-config", "{\"a\":1}"}) {
		t.Fatalf("cmd %#v missing --mcp-config inline entry", cmd)
	}
	if !containsSubsequence(cmd, []string{"--mcp-config", "/p/m.json"}) {
		t.Fatalf("cmd %#v missing --mcp-config path entry", cmd)
	}
	if !contains(cmd, "--strict-mcp-config") {
		t.Fatalf("cmd %#v missing --strict-mcp-config", cmd)
	}
	if !containsSubsequence(cmd, []string{"--plugin-dir", "/local/plugin"}) {
		t.Fatalf("cmd %#v missing --plugin-dir for local path", cmd)
	}
	if !containsSubsequence(cmd, []string{"--plugin-url", "https://example.com/p.zip"}) {
		t.Fatalf("cmd %#v missing --plugin-url for URL", cmd)
	}
}

// TestIsPluginURLCaseInsensitive: URL schemes are case-insensitive (RFC 3986),
// so HTTPS:// and HtTp:// must route to --plugin-url, not --plugin-dir.
func TestIsPluginURLCaseInsensitive(t *testing.T) {
	for _, in := range []string{"https://example.com/p.zip", "HTTPS://example.com/p.zip", "HtTp://example.com/p.zip", "http://example.com/p.zip"} {
		if !isPluginURL(in) {
			t.Errorf("isPluginURL(%q) = false, want true (scheme is case-insensitive)", in)
		}
	}
	for _, in := range []string{"/local/plugin", "./relative", "ftp://example.com/p.zip", "example.com/p.zip"} {
		if isPluginURL(in) {
			t.Errorf("isPluginURL(%q) = true, want false", in)
		}
	}
}

// TestGetLaunchCommandNoMCPFlagsWhenUnset: a config with no MCP/plugin
// configuration emits nothing, so an unset role inherits the global set.
func TestGetLaunchCommandNoMCPFlagsWhenUnset(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--mcp-config", "--strict-mcp-config", "--plugin-dir", "--plugin-url"} {
		if contains(cmd, flag) {
			t.Fatalf("cmd %#v unexpectedly contains %s", cmd, flag)
		}
	}
}

// TestGetLaunchCommandMCPStrictAlone: Strict with no configs is valid isolation
// and emits just --strict-mcp-config.
func TestGetLaunchCommandMCPStrictAlone(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{MCP: &domain.MCPConfig{Strict: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(cmd, "--strict-mcp-config") {
		t.Fatalf("cmd %#v missing --strict-mcp-config", cmd)
	}
	if contains(cmd, "--mcp-config") {
		t.Fatalf("cmd %#v unexpectedly contains --mcp-config", cmd)
	}
}

// TestGetRestoreCommandReappliesMCPAndPluginFlags: like the system prompt, MCP
// and plugin flags are rebuilt from flags on resume, so a restored worker keeps
// its scoped MCP set and plugins.
func TestGetRestoreCommandReappliesMCPAndPluginFlags(t *testing.T) {
	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Config: ports.AgentConfig{
			MCP:        &domain.MCPConfig{Configs: []string{"{\"a\":1}"}, Strict: true},
			PluginDirs: []string{"https://example.com/p.zip"},
		},
		Session: ports.SessionRef{
			ID:       "sess-r",
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore = (ok=%v, err=%v), want ok", ok, err)
	}
	if !containsSubsequence(cmd, []string{"--mcp-config", "{\"a\":1}"}) {
		t.Fatalf("restore cmd %#v missing --mcp-config", cmd)
	}
	if !contains(cmd, "--strict-mcp-config") {
		t.Fatalf("restore cmd %#v missing --strict-mcp-config", cmd)
	}
	if !containsSubsequence(cmd, []string{"--plugin-url", "https://example.com/p.zip"}) {
		t.Fatalf("restore cmd %#v missing --plugin-url", cmd)
	}
	// Flags must precede --resume.
	if !flagBefore(cmd, "--strict-mcp-config", "--resume") {
		t.Fatalf("restore cmd %#v: MCP flag not before --resume", cmd)
	}
}

// claudeEncodedProjectDir mirrors Claude Code's real transcript-directory
// naming: EVERY non-alphanumeric character in the absolute workspace path
// becomes "-", including underscores and dots. This is deliberately a
// test-local mirror of the provider's rule, kept independent from the
// production probe (which must not depend on the encoding at all).
func claudeEncodedProjectDir(workspacePath string) string {
	var b strings.Builder
	for _, r := range workspacePath {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// writeFakeTranscript creates a transcript file where Claude Code itself
// would have put it when resuming sessionID in workspacePath, under a HOME
// this test controls. Returns the HOME to set via t.Setenv.
func writeFakeTranscript(t *testing.T, workspacePath, sessionID string) (home string) {
	t.Helper()
	home = t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", claudeEncodedProjectDir(workspacePath))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestGetRestoreCommandResumesWhenWorkspacePathHasUnderscores reproduces the
// "Session ID <uuid> is already in use" incident: Claude Code encodes EVERY
// non-alphanumeric character of the workspace path (underscores included) into
// the projects/<dir> name. A transcript probe that mirrored only "/" and "."
// computed a different directory, concluded the conversation was missing, and
// routed restore into the fresh-launch fallback — which pins the deterministic
// --session-id over an existing transcript. The create-only --session-id flag
// then makes claude exit immediately with "already in use", leaving the
// session exited-but-not-terminated forever. The probe must find the
// transcript under Claude's real encoding.
func TestGetRestoreCommandResumesWhenWorkspacePathHasUnderscores(t *testing.T) {
	workspace := "/ws/zai_python_helper/orchestrator"
	t.Setenv("HOME", writeFakeTranscript(t, workspace, "claude-native-1"))

	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Session: ports.SessionRef{
			ID:            "sess-r",
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore = (ok=%v, err=%v), want ok — transcript exists under Claude's encoding", ok, err)
	}
	want := []string{"claude", "--permission-mode", "bypassPermissions", "--resume", "claude-native-1"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetRestoreCommandReadsAgentSessionID_TranscriptPresent(t *testing.T) {
	// With a workspace path AND a matching transcript on disk, restore
	// proceeds exactly as before: hook-captured id wins, --resume is issued.
	workspace := "/ws/sess-r"
	t.Setenv("HOME", writeFakeTranscript(t, workspace, "claude-native-1"))

	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Session: ports.SessionRef{
			ID:            "sess-r",
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore = (ok=%v, err=%v), want ok", ok, err)
	}
	want := []string{"claude", "--permission-mode", "bypassPermissions", "--resume", "claude-native-1"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
}

// TestGetRestoreCommandFalseWhenTranscriptMissing is the regression test for
// the bug this file's claudeTranscriptExists guards against: a non-blank
// agent session id (hook-captured or derived) is not proof a transcript
// exists. After a reboot, or when the SessionStart hook never fired, AO could
// hold an id with nothing on disk behind it. Before the fix, GetRestoreCommand
// returned ok=true unconditionally here, so `claude --resume <id>` would exit
// 1 with "No conversation found with session ID" and the manager's
// fresh-launch fallback (which only runs when ok=false) was unreachable.
func TestGetRestoreCommandFalseWhenTranscriptMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no transcript written anywhere under this HOME

	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Session: ports.SessionRef{
			ID:            "sess-r",
			WorkspacePath: "/ws/sess-r",
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"},
		},
	})
	if err != nil || ok || cmd != nil {
		t.Fatalf("restore = (%#v, ok=%v, err=%v), want (nil, false, nil)", cmd, ok, err)
	}
}

// TestGetRestoreCommandFalseWhenDerivedUUIDTranscriptMissing covers the
// pre-hook fallback path specifically: claudeSessionUUID is a guess (an AO
// session that never got far enough to run the SessionStart hook), so it must
// be held to the same transcript-existence check as a hook-captured id.
func TestGetRestoreCommandFalseWhenDerivedUUIDTranscriptMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Session:     ports.SessionRef{ID: "sess-r", WorkspacePath: "/ws/sess-r"},
	})
	if err != nil || ok || cmd != nil {
		t.Fatalf("restore = (%#v, ok=%v, err=%v), want (nil, false, nil)", cmd, ok, err)
	}
}

// TestGetRestoreCommandFallsBackToDerivedUUID_TranscriptPresent is the
// updated form of the original "falls back to derived UUID" test: it now
// pins a HOME with a matching transcript so the assertion also exercises
// claudeTranscriptExists on the success path, not just the no-workspace-path
// case covered by TestGetRestoreCommandFallsBackToDerivedUUID below.
func TestGetRestoreCommandFallsBackToDerivedUUID_TranscriptPresent(t *testing.T) {
	workspace := "/ws/sess-r"
	t.Setenv("HOME", writeFakeTranscript(t, workspace, claudeSessionUUID("sess-r")))

	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Session:     ports.SessionRef{ID: "sess-r", WorkspacePath: workspace},
	})
	if err != nil || !ok {
		t.Fatalf("restore = (ok=%v, err=%v), want ok", ok, err)
	}
	want := []string{"claude", "--permission-mode", "bypassPermissions", "--resume", claudeSessionUUID("sess-r")}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
}

// Everything after the `--` separator is a positional argument, not a flag, so
// MCP/plugin flags emitted after it are silently ignored by the CLI: the
// session starts with no scoped MCP set and no plugins, which is the whole
// point of the config. This regressed once — the flags used to be appended to
// the finished argv, landing after `-- <prompt>` whenever a prompt was present,
// and the tests above never caught it because they only assert the flag pairs
// appear somewhere. Routing them through ProviderArgs fixed it by construction.
func TestProfileFlagsPrecedePromptSeparator(t *testing.T) {
	cfg := ports.AgentConfig{
		MCP:        &domain.MCPConfig{Configs: []string{`{"a":1}`}, Strict: true},
		PluginDirs: []string{"/local/plugin", "https://example.com/p.zip"},
	}
	p := &Plugin{resolvedBinary: "claude"}

	launch, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID: "ao-1", Prompt: "do the thing", Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			ID:       "ao-1",
			Metadata: map[string]string{"agentSessionId": "claude-native-1"},
		},
		Prompt: "continue",
		Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("restore not ok")
	}

	for _, tc := range []struct {
		name string
		cmd  []string
	}{
		{"launch", launch},
		{"restore", restore},
	} {
		for _, flag := range []string{"--mcp-config", "--strict-mcp-config", "--plugin-dir", "--plugin-url"} {
			if flag == "--plugin-url" && tc.name == "restore" {
				continue // restore fixture carries only the local plugin dir
			}
			if !flagBefore(tc.cmd, flag, "--") {
				t.Errorf("%s cmd %#v: %s is not before --, so the CLI ignores it", tc.name, tc.cmd, flag)
			}
		}
	}
}

// flagBefore reports whether flag appears before marker in cmd. Restore pins
// marker="--resume" (the resume target stays last); both commands pin
// marker="--" (a flag after it is a positional argument, not a flag).
func flagBefore(cmd []string, flag, marker string) bool {
	for _, v := range cmd {
		if v == marker {
			return false
		}
		if v == flag {
			return true
		}
	}
	return false
}
