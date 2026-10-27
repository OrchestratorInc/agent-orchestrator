package agentauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestContinueProviderLoginWritesNativeConfigAndGlobalEnv(t *testing.T) {
	home := t.TempDir()
	previousHome := continueConfigHome
	continueConfigHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { continueConfigHome = previousHome })
	previousPublish := publishProviderKey
	publishProviderKey = func(_ context.Context, name, value string) error {
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
