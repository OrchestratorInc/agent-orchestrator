package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentSandboxSupport = (*Plugin)(nil)

func TestPrepareSandboxSeedsConfigAndToken(t *testing.T) {
	data, project := t.TempDir(), t.TempDir()
	tokenPath := localOAuthTokenPath(data)
	_ = os.MkdirAll(filepath.Dir(tokenPath), 0o700)
	_ = os.WriteFile(tokenPath, []byte("tok\n"), 0o600)
	env := map[string]string{}
	p := &Plugin{}
	if err := p.PrepareSandbox(env, data, project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(project, "claude")
	if env["CLAUDE_CONFIG_DIR"] != configDir || env["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" {
		t.Fatalf("env = %#v", env)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
		Projects               map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.HasCompletedOnboarding || !cfg.Projects["/w/s1"].HasTrustDialogAccepted {
		t.Fatalf("claude config = %s", raw)
	}
}

func TestPrepareSandboxKeepsProjectAPIKey(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "sk"}
	if err := (&Plugin{}).PrepareSandbox(env, t.TempDir(), t.TempDir(), "/w"); err != nil {
		t.Fatal(err)
	}
	if _, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Fatalf("token injected over explicit API key: %#v", env)
	}
}

func TestPrepareSandboxWithoutCredentialFails(t *testing.T) {
	err := (&Plugin{}).PrepareSandbox(map[string]string{}, t.TempDir(), t.TempDir(), "/w")
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured", err)
	}
	if !strings.Contains(err.Error(), "claude setup-token") {
		t.Fatalf("error does not name setup-token remediation: %v", err)
	}
	if strings.Contains(err.Error(), "claude-login") {
		t.Fatalf("error still points at hidden claude-login: %v", err)
	}
}

func TestPrepareSandboxKeepsExistingConfigKeys(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(project, "claude")
	_ = os.MkdirAll(configDir, 0o700)
	existing := `{"theme":"dark","projects":{"/other":{"hasTrustDialogAccepted":true}}}`
	_ = os.WriteFile(filepath.Join(configDir, ".claude.json"), []byte(existing), 0o600)
	env := map[string]string{"ANTHROPIC_API_KEY": "sk"}
	if err := (&Plugin{}).PrepareSandbox(env, t.TempDir(), project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["theme"] != "dark" || cfg["hasCompletedOnboarding"] != true {
		t.Fatalf("config lost keys: %s", raw)
	}
	projects, _ := cfg["projects"].(map[string]any)
	other, _ := projects["/other"].(map[string]any)
	s1, _ := projects["/w/s1"].(map[string]any)
	if other["hasTrustDialogAccepted"] != true || s1["hasTrustDialogAccepted"] != true {
		t.Fatalf("projects lost entries: %s", raw)
	}
}

func TestPrepareSandboxCorruptConfigFailsUnchanged(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(project, "claude")
	_ = os.MkdirAll(configDir, 0o700)
	configPath := filepath.Join(configDir, ".claude.json")
	_ = os.WriteFile(configPath, []byte("{not json"), 0o600)
	env := map[string]string{"ANTHROPIC_API_KEY": "sk"}
	if err := (&Plugin{}).PrepareSandbox(env, t.TempDir(), project, "/w/s1"); err == nil {
		t.Fatal("expected error for corrupt config")
	}
	raw, _ := os.ReadFile(configPath)
	if string(raw) != "{not json" {
		t.Fatalf("corrupt config was rewritten: %q", raw)
	}
}

func TestPrepareSandboxKeepsExplicitOAuthToken(t *testing.T) {
	data := t.TempDir()
	tokenPath := localOAuthTokenPath(data)
	_ = os.MkdirAll(filepath.Dir(tokenPath), 0o700)
	_ = os.WriteFile(tokenPath, []byte("tok\n"), 0o600)
	env := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "mine"}
	if err := (&Plugin{}).PrepareSandbox(env, data, t.TempDir(), "/w"); err != nil {
		t.Fatal(err)
	}
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "mine" {
		t.Fatalf("explicit token overwritten: %q", env["CLAUDE_CODE_OAUTH_TOKEN"])
	}
}

// The container owns the claude config dir, so it can plant .claude.json as a
// symlink to a host file. PrepareSandbox must neither copy that file into the
// container-visible dir nor write through the link.
func TestPrepareSandboxDoesNotFollowPlantedConfigSymlink(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "creds.json")
	const secretBody = `{"refresh_token":"HOST-SECRET"}`
	if err := os.WriteFile(secret, []byte(secretBody), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	configDir := filepath.Join(project, "claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, ".claude.json")
	if err := os.Symlink(secret, configPath); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "sk"}
	if err := (&Plugin{}).PrepareSandbox(env, t.TempDir(), project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(secret); string(b) != secretBody {
		t.Fatalf("outside file modified: %q", b)
	}
	fi, err := os.Lstat(configPath)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("config = %v, %v; want a regular file", fi, err)
	}
	raw, _ := os.ReadFile(configPath)
	if strings.Contains(string(raw), "HOST-SECRET") {
		t.Fatalf("host secret copied into the sandbox config: %s", raw)
	}
	assertSandboxConfigSeeded(t, raw, "/w/s1")
}

func TestPrepareSandboxReplacesConfigDirectory(t *testing.T) {
	project := t.TempDir()
	configPath := filepath.Join(project, "claude", ".claude.json")
	if err := os.MkdirAll(filepath.Join(configPath, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&Plugin{}).PrepareSandbox(map[string]string{"ANTHROPIC_API_KEY": "sk"}, t.TempDir(), project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	assertSandboxConfigSeeded(t, raw, "/w/s1")
}

// A symlinked claude/ dir would point the config root outside the sandbox.
func TestPrepareSandboxReplacesSymlinkedConfigDir(t *testing.T) {
	outside := t.TempDir()
	project := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := (&Plugin{}).PrepareSandbox(map[string]string{"ANTHROPIC_API_KEY": "sk"}, t.TempDir(), project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the sandbox: %v", entries)
	}
	fi, err := os.Lstat(filepath.Join(project, "claude"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("claude dir = %v, %v; want a real directory", fi, err)
	}
	raw, err := os.ReadFile(filepath.Join(project, "claude", ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertSandboxConfigSeeded(t, raw, "/w/s1")
}

// The container can write `null` into its config; that must not panic the
// daemon on the next launch.
func TestPrepareSandboxNullConfigDoesNotPanic(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(project, "claude")
	_ = os.MkdirAll(configDir, 0o700)
	_ = os.WriteFile(filepath.Join(configDir, ".claude.json"), []byte("null"), 0o600)
	if err := (&Plugin{}).PrepareSandbox(map[string]string{"ANTHROPIC_API_KEY": "sk"}, t.TempDir(), project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(configDir, ".claude.json"))
	assertSandboxConfigSeeded(t, raw, "/w/s1")
}

func TestEnsureWorkspaceTrustedNullConfigDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	_ = os.WriteFile(path, []byte("null"), 0o600)
	if err := ensureWorkspaceTrusted(path, "/ws"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var cfg struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || !cfg.Projects["/ws"].HasTrustDialogAccepted {
		t.Fatalf("config = %s (%v)", raw, err)
	}
}

func assertSandboxConfigSeeded(t *testing.T, raw []byte, workspace string) {
	t.Helper()
	var cfg struct {
		HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
		Projects               map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config %q: %v", raw, err)
	}
	if !cfg.HasCompletedOnboarding || !cfg.Projects[workspace].HasTrustDialogAccepted {
		t.Fatalf("config not seeded: %s", raw)
	}
}

// A sandboxed launch runs Claude from the image, so the host binary is never
// resolved and argv[0] is the bare in-image name.
func TestSandboxedLaunchAndRestoreUseImageBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	p := &Plugin{}
	argv, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{SessionID: "s1", WorkspacePath: "/w", Sandboxed: true})
	if err != nil || len(argv) == 0 || argv[0] != "claude" {
		t.Fatalf("launch argv = %q, err = %v; want argv[0] claude", argv, err)
	}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Sandboxed: true,
		Session: ports.SessionRef{ID: "s1", WorkspacePath: "/w", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: SessionUUID("s1")}}})
	if err != nil || !ok || len(cmd) == 0 || cmd[0] != "claude" {
		t.Fatalf("restore argv = %q ok=%v err=%v; want argv[0] claude", cmd, ok, err)
	}
	if p.resolvedBinary != "" {
		t.Fatalf("host binary resolved for a sandboxed launch: %q", p.resolvedBinary)
	}
}
