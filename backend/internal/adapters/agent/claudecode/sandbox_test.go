package claudecode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
}
