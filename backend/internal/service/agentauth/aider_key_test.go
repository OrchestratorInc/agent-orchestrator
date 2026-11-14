package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"

	"gopkg.in/yaml.v3"
)

func stubAiderHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	previous := aiderConfigHome
	aiderConfigHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { aiderConfigHome = previous })
	return home
}

func aiderConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".aider.conf.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid YAML after save: %v\n%s", err, data)
	}
	return config
}

func TestAiderKeySaveCreatesDocumentedEntries(t *testing.T) {
	home := stubAiderHome(t)
	svc := New(nil, nil)
	t.Cleanup(svc.Close)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("API key save launched a command")
		return nil
	}
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_openai_key", "sk-openai"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_anthropic_key", "sk-anthropic"); err != nil {
		t.Fatal(err)
	}
	config := aiderConfig(t, home)
	if config["openai-api-key"] != "sk-openai" || config["anthropic-api-key"] != "sk-anthropic" {
		t.Fatalf("dedicated key entries missing: %v", config)
	}
	if info, err := os.Stat(filepath.Join(home, ".aider.conf.yml")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("config permissions: %v %v", info, err)
	}
}

func TestAiderKeySaveUpsertsProviderListAndPreservesSettings(t *testing.T) {
	home := stubAiderHome(t)
	path := filepath.Join(home, ".aider.conf.yml")
	existing := "# Aider configuration\nmodel: anthropic/claude-sonnet-4-5\nauto-commits: false\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil)
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_openrouter_key", "sk-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_deepseek_key", "sk-deep"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_openrouter_key", "sk-two"); err != nil {
		t.Fatal(err)
	}
	config := aiderConfig(t, home)
	if config["model"] != "anthropic/claude-sonnet-4-5" || config["auto-commits"] != false {
		t.Fatalf("existing settings lost: %v", config)
	}
	entries, _ := config["api-key"].([]any)
	var openrouter, deepseek []string
	for _, entry := range entries {
		text, _ := entry.(string)
		if strings.HasPrefix(text, "openrouter=") {
			openrouter = append(openrouter, text)
		}
		if text == "deepseek=sk-deep" {
			deepseek = append(deepseek, text)
		}
	}
	if len(openrouter) != 1 || openrouter[0] != "openrouter=sk-two" || len(deepseek) != 1 {
		t.Fatalf("provider list entries wrong: %v", entries)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# Aider configuration") {
		t.Fatal("user comment dropped from config")
	}
}

func TestAiderKeySaveWidensScalarProviderEntry(t *testing.T) {
	home := stubAiderHome(t)
	path := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(path, []byte("api-key: gemini=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil)
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_gemini_key", "sk-new"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(t.Context(), "aider", "aider_openrouter_key", "sk-or"); err != nil {
		t.Fatal(err)
	}
	config := aiderConfig(t, home)
	entries, _ := config["api-key"].([]any)
	if len(entries) != 2 || entries[0] != "gemini=sk-new" || entries[1] != "openrouter=sk-or" {
		t.Fatalf("scalar entry not widened safely: %v", entries)
	}
}

func TestAiderKeySaveRejectsUnsafeStoreWithoutModifyingIt(t *testing.T) {
	for name, content := range map[string]string{
		"invalid yaml":  "\tmodel: [broken\n",
		"sequence root": "- one\n- two\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := stubAiderHome(t)
			path := filepath.Join(home, ".aider.conf.yml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			svc := New(nil, nil)
			t.Cleanup(svc.Close)
			_, err := svc.StartLogin(t.Context(), "aider", "aider_openai_key", "sk-openai")
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_STORE_INVALID" {
				t.Fatalf("err=%v", err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != content {
				t.Fatal("rejected store was modified")
			}
		})
	}
}

func TestAiderKeySecretNeverAppearsInOperationOrCommand(t *testing.T) {
	stubAiderHome(t)
	svc := New(nil, nil)
	t.Cleanup(svc.Close)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("API key save launched a command")
		return nil
	}
	result, err := svc.StartLogin(t.Context(), "aider", "aider_deepseek_key", "sk-secret-value")
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "sk-secret-value") {
		t.Fatal("credential leaked in operation")
	}
}
