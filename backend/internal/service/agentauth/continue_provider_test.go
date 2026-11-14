package agentauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestContinueProviderLoginWritesNativeConfigAndGlobalEnv(t *testing.T) {
	home := t.TempDir()
	previousHome := continueConfigHome
	continueConfigHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { continueConfigHome = previousHome })
	previousPublish := publishProviderKey
	publishProviderKey = func(_ context.Context, name, value, _ string) error {
		if name != "ANTHROPIC_API_KEY" || value != "test-secret" {
			t.Fatal("unexpected device publication")
		}
		return nil
	}
	t.Cleanup(func() { publishProviderKey = previousPublish })
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := filepath.Join(home, ".continue")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("name: My Config\nversion: 1.0.0\nschema: v1\n# preserve this\nmodels:\n  - name: Existing\n    provider: openai\n    model: gpt-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("EXISTING_KEY=keep\nexport ANTHROPIC_API_KEY = old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("cn"), nil, nil, t.TempDir())
	if _, err := svc.StartLogin(t.Context(), "continue", "env_ANTHROPIC_API_KEY", "test-secret"); err == nil {
		t.Fatal("accepted Continue provider without model")
	}
	result, err := svc.StartLoginWithModel(t.Context(), "continue", "env_ANTHROPIC_API_KEY", "test-secret", "claude-sonnet-4-6")
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("Continue login = %+v, %v", result, err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "test-secret") || !strings.Contains(string(config), "# preserve this") {
		t.Fatalf("Continue config lost comments or contains secret: %s", config)
	}
	var decoded struct {
		Name   string `yaml:"name"`
		Models []struct {
			Provider string `yaml:"provider"`
			Model    string `yaml:"model"`
			APIKey   string `yaml:"apiKey"`
		} `yaml:"models"`
	}
	if err := yaml.Unmarshal(config, &decoded); err != nil || decoded.Name != "My Config" || len(decoded.Models) != 2 ||
		decoded.Models[1].Provider != "anthropic" || decoded.Models[1].Model != "claude-sonnet-4-6" || decoded.Models[1].APIKey != "${{ secrets.ANTHROPIC_API_KEY }}" {
		t.Fatalf("Continue config = %+v, %v", decoded, err)
	}
	envData, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil || string(envData) != "EXISTING_KEY=keep\nANTHROPIC_API_KEY=test-secret\n" {
		t.Fatalf("Continue global env = %q, %v", envData, err)
	}
}

func TestContinueProviderFollowsContinuesOwnConfigChoice(t *testing.T) {
	home := t.TempDir()
	previousHome := continueConfigHome
	continueConfigHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { continueConfigHome = previousHome })
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	code := func(err error) string {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			return apiErr.Code
		}
		return ""
	}
	// Continue falls back to config.json only while config.yaml is missing.
	legacy := filepath.Join(home, ".continue")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveContinueProviderUndoable(t.Context(), "OPENAI_API_KEY", "gpt-5", "test-secret"); code(err) != "AGENT_AUTH_STORE_LEGACY" {
		t.Fatalf("saved beside a legacy config.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("created a config.yaml that hides config.json")
	}
	// CONTINUE_GLOBAL_DIR moves the whole global folder.
	global := t.TempDir()
	t.Setenv("CONTINUE_GLOBAL_DIR", global)
	if _, err := saveContinueProviderUndoable(t.Context(), "OPENAI_API_KEY", "gpt-5", "test-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(global, ".env")); err != nil {
		t.Fatalf("key not saved in CONTINUE_GLOBAL_DIR: %v", err)
	}
	// A value other models share through an anchor is never rewritten; an
	// alias becomes a plain value and the anchor it pointed at stays valid.
	shared := "name: c\nversion: 0.0.1\nschema: v1\nmodels:\n  - name: gpt-5\n    provider: openai\n    model: gpt-5\n    apiKey: &shared ${{ secrets.OTHER_KEY }}\n  - name: other\n    provider: openai\n    model: other\n    apiKey: *shared\n"
	configPath := filepath.Join(global, "config.yaml")
	if err := os.WriteFile(configPath, []byte(shared), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveContinueProviderUndoable(t.Context(), "OPENAI_API_KEY", "gpt-5", "test-secret"); code(err) != "AGENT_AUTH_STORE_SHARED" {
		t.Fatalf("rewrote an anchored value: %v", err)
	}
	if data, _ := os.ReadFile(configPath); string(data) != shared {
		t.Fatalf("changed a refused config: %s", data)
	}
	if _, err := saveContinueProviderUndoable(t.Context(), "OPENAI_API_KEY", "other", "test-secret"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	var decoded map[string]any
	if err != nil || yaml.Unmarshal(data, &decoded) != nil || !strings.Contains(string(data), "&shared") {
		t.Fatalf("config after replacing an alias = %s, %v", data, err)
	}
}
