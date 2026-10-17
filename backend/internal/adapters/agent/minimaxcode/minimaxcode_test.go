package minimaxcode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLaunchContract(t *testing.T) {
	p := New()
	p.resolvedBinary = "/installed/mcode"
	cfg := ports.LaunchConfig{DataDir: t.TempDir(), SessionID: "test-1", WorkspacePath: t.TempDir(), Prompt: "--help", SystemPromptFile: "/private/instructions", Permissions: ports.PermissionModeAuto}
	got, err := p.GetLaunchCommand(context.Background(), cfg)
	want := []string{"/installed/mcode", "--", "--help"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("launch=%q, %v; want %q", got, err, want)
	}
	cfg.Permissions = ports.PermissionModeAcceptEdits
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
		t.Fatal("unsupported accept-edits accepted")
	}
	cfg.Permissions = ports.PermissionModeDefault
	cfg.SystemPromptFile = ""
	cfg.SystemPrompt = "private"
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
		t.Fatal("inline private prompt accepted without private file")
	}
}

func TestProfilePathAcceptsAOIDsWithoutAllowingTraversal(t *testing.T) {
	data := t.TempDir()
	for _, id := range []string{"acme.dev-2", "acme.dev.tools-3", "session-1"} {
		got, err := profilePath(data, id)
		if err != nil || got != filepath.Join(data, "agents", adapterID, id) {
			t.Fatalf("profilePath(%q) = %q, %v", id, got, err)
		}
	}
	for _, id := range []string{"", ".", "..", "../escape", "a/b", `a\b`, ".hidden", "a..b", "a."} {
		if _, err := profilePath(data, id); err == nil {
			t.Fatalf("accepted unsafe AO session ID %q", id)
		}
	}
}

func TestTUIProfileRefreshesCredentialsWithoutReplacingNativeState(t *testing.T) {
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(source, "config.yaml"), "defaultModel: p/first\npermissionMode: auto\n")
	write(filepath.Join(source, "auth", "token"), "original-token")
	write(filepath.Join(source, "auth", "revoked"), "revoked-token")
	cfg := ports.WorkspaceHookConfig{
		DataDir: t.TempDir(), SessionID: "acme.dev-2", WorkspacePath: t.TempDir(),
		Config: ports.AgentConfig{Permissions: ports.PermissionModeBypassPermissions},
	}
	plugin := New()
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
	retained := map[string]string{
		"v2/history-proof": "native-history", "ao-native-id": "native-identity",
		"permission.json": "private-policy", "AGENTS.md": "private-rules",
	}
	for name, content := range retained {
		write(filepath.Join(profile, name), content)
	}
	updated := "defaultModel: p/second\npermissionMode: auto\ncustom_provider:\n  new-provider:\n    apiKey: rotated-key\n"
	write(filepath.Join(source, "config.yaml"), updated)
	write(filepath.Join(source, "auth", "token"), "rotated-token")
	if err := os.Remove(filepath.Join(source, "auth", "revoked")); err != nil {
		t.Fatal(err)
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for name, content := range retained {
		got, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil || string(got) != content {
			t.Fatalf("native state %s changed: %q, %v", name, got, err)
		}
	}
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(source, "config.yaml"), updated},
		{filepath.Join(profile, "auth", "token"), "rotated-token"},
	} {
		got, err := os.ReadFile(tc.path)
		if err != nil || string(got) != tc.want {
			t.Fatalf("credential/source mismatch at %s: %q, %v", tc.path, got, err)
		}
	}
	config, err := os.ReadFile(filepath.Join(profile, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"defaultModel: p/second", "apiKey: rotated-key", "permissionMode: bypassPermissions"} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("refreshed private configuration omitted %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(profile, "auth", "revoked")); !os.IsNotExist(err) {
		t.Fatalf("revoked credential retained: %v", err)
	}
	write(filepath.Join(source, "config.yaml"), "invalid: [")
	write(filepath.Join(source, "auth", "token"), "must-not-replace-snapshot")
	if err := plugin.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("accepted malformed source configuration during refresh")
	}
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(profile, "config.yaml"), string(config)},
		{filepath.Join(profile, "auth", "token"), "rotated-token"},
	} {
		got, err := os.ReadFile(tc.path)
		if err != nil || string(got) != tc.want {
			t.Fatalf("failed refresh changed previous snapshot at %s: %q, %v", tc.path, got, err)
		}
	}
}

func TestRestoreFailsClosed(t *testing.T) {
	p := New()
	p.resolvedBinary = "mcode"
	cfg := ports.RestoreConfig{DataDir: t.TempDir(), Session: ports.SessionRef{ID: "test", WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "mvs_01234567890123456789012345678901"}}}
	if cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg); err == nil || ok || cmd != nil {
		t.Fatalf("missing history: %q %v %v", cmd, ok, err)
	}
	cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = "../escape"
	if _, ok, err := p.GetRestoreCommand(context.Background(), cfg); err == nil || ok {
		t.Fatalf("unsafe id: %v %v", ok, err)
	}
}

func TestPrivateProfileSnapshot(t *testing.T) {
	source := t.TempDir()
	original := []byte("defaultModel: custom_provider:test/model\npermissionMode: auto\ncustom_provider:\n  test:\n    apiKey: fixture-secret\nuserSetting: keep\n")
	if err := os.WriteFile(filepath.Join(source, "config.yaml"), original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINIMAX_DATA_DIR", source)
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "session-1", WorkspacePath: t.TempDir(), Config: ports.AgentConfig{Permissions: ports.PermissionModeDefault}}
	p := New()
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(source, "config.yaml"))
	if !reflect.DeepEqual(got, original) {
		t.Fatal("source profile changed")
	}
	profile, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(profile, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == string(original) {
		t.Fatal("permission override not isolated")
	}
	st, _ := os.Stat(filepath.Join(profile, "config.yaml"))
	if st.Mode().Perm() != 0600 {
		t.Fatalf("config mode %o", st.Mode().Perm())
	}
	cfg.Config.Permissions = ports.PermissionModeBypassPermissions
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(profile, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "permissionMode: bypassPermissions") {
		t.Fatalf("bypass permission mode was not serialized natively: %s", b)
	}
	cfg.Config.Permissions = ports.PermissionModeDefault
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SessionID = "../escape"
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("unsafe AO id accepted")
	}
}

func TestProfileFailureCanRetryAndPreservesResources(t *testing.T) {
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	resource := filepath.Join(source, "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(resource), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("user skill"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(source, "config.yaml")
	if err := os.WriteFile(config, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "retry", WorkspacePath: t.TempDir()}
	p := New()
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("accepted corrupt config")
	}
	target, _ := profilePath(cfg.DataDir, cfg.SessionID)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("failed snapshot left final profile: %v", err)
	}
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "skills", "custom", "SKILL.md"))
	if err != nil || string(got) != "user skill" {
		t.Fatalf("resource lost: %s, %v", got, err)
	}
	cfg.Config.Permissions = ports.PermissionMode("unexpected")
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("unknown permissions accepted")
	}
}

func TestSnapshotPreservesSubagentsAndExecutableResources(t *testing.T) {
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	for name, mode := range map[string]os.FileMode{"subagents/custom.md": 0600, "plugins/user/hooks/run.sh": 0700, "AGENTS.md": 0600, "tui/keybindings.json": 0600} {
		file := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("user resource"), mode); err != nil {
			t.Fatal(err)
		}
	}
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "resources", WorkspacePath: t.TempDir()}
	p := New()
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	target, _ := profilePath(cfg.DataDir, cfg.SessionID)
	for name, mode := range map[string]os.FileMode{"subagents/custom.md": 0600, "plugins/user/hooks/run.sh": 0700, "AGENTS.md": 0600, "tui/keybindings.json": 0600} {
		info, err := os.Stat(filepath.Join(target, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("resource %s mode mismatch: %v", name, err)
		}
	}
	if err := p.CleanupWorkspace(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("failed-preparation cleanup removed persistent profile: %v", err)
	}
	if err := os.Remove(cfg.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanupWorkspace(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("cleanup after workspace deletion retained profile: %v", err)
	}
}

// snapshotRaceContext replaces the source when the walk enters its callback,
// after the walker inspected it but before the snapshot opens it.
type snapshotRaceContext struct {
	context.Context
	replace func()
}

func (c *snapshotRaceContext) Err() error {
	if c.replace != nil {
		replace := c.replace
		c.replace = nil
		replace()
	}
	return c.Context.Err()
}

func TestSnapshotRefusesConcurrentSourceEscape(t *testing.T) {
	source := filepath.Join(t.TempDir(), "AGENTS.md")
	outside := filepath.Join(t.TempDir(), "private.md")
	target := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(source, []byte("intended resource"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("must not be copied"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := &snapshotRaceContext{Context: context.Background(), replace: func() {
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, source); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}}
	if err := copyProfileTree(ctx, source, target); err == nil {
		t.Fatal("snapshot followed a concurrently replaced source outside its root")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("snapshot wrote a replaced source: %v", err)
	}
}
