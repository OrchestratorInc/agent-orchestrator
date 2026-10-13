package claudecode

import (
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
