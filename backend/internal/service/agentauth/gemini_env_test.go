package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func stubGeminiHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	previous := geminiConfigHome
	geminiConfigHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { geminiConfigHome = previous })
	return home
}

func geminiEnv(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".gemini", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGeminiKeySaveCreatesDocumentedEnvFile(t *testing.T) {
	home := stubGeminiHome(t)
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("API key save launched a command")
		return nil
	}
	result, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-gemini")
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := geminiEnv(t, home); !strings.Contains(got, "GEMINI_API_KEY=sk-gemini\n") {
		t.Fatalf("env file wrong: %q", got)
	}
	var settings struct {
		Security struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
	}
	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil || json.Unmarshal(data, &settings) != nil || settings.Security.Auth.SelectedType != "gemini-api-key" {
		t.Fatalf("auth mode not selected: %s, %v", data, err)
	}
}

func TestGeminiKeySaveReplacesKeyLineAndPreservesOthers(t *testing.T) {
	home := stubGeminiHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".gemini", ".env")
	if err := os.WriteFile(path, []byte("# user settings\nGOOGLE_CLOUD_PROJECT=my-project\nGEMINI_API_KEY=old-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-new"); err != nil {
		t.Fatal(err)
	}
	got := geminiEnv(t, home)
	if strings.Contains(got, "old-key") || !strings.Contains(got, "GEMINI_API_KEY=sk-new\n") {
		t.Fatalf("key not replaced: %q", got)
	}
	if !strings.Contains(got, "GOOGLE_CLOUD_PROJECT=my-project") || !strings.Contains(got, "# user settings") {
		t.Fatalf("other variables lost: %q", got)
	}
	if strings.Count(got, "GEMINI_API_KEY=") != 1 {
		t.Fatalf("duplicate key entries: %q", got)
	}
}

func TestGeminiKeySaveMatchesExportPrefixedAssignment(t *testing.T) {
	home := stubGeminiHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".gemini", ".env")
	if err := os.WriteFile(path, []byte("export GEMINI_API_KEY=old"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-new"); err != nil {
		t.Fatal(err)
	}
	got := geminiEnv(t, home)
	if strings.Contains(got, "export GEMINI_API_KEY") || !strings.Contains(got, "GEMINI_API_KEY=sk-new\n") {
		t.Fatalf("export assignment not replaced: %q", got)
	}
}

func TestGeminiKeySaveRejectsUnsafeStore(t *testing.T) {
	home := stubGeminiHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".gemini", ".env")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 65*1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	_, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-new")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_STORE_UNSAFE" {
		t.Fatalf("err=%v", err)
	}
}

func TestGeminiKeySavePreservesSettingsAndSwitchesAuthMode(t *testing.T) {
	home := stubGeminiHome(t)
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"ui":{"theme":"custom"},"security":{"auth":{"selectedType":"oauth-personal","useExternal":true},"folderTrust":{"enabled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-new"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	security := settings["security"].(map[string]any)
	auth := security["auth"].(map[string]any)
	if auth["selectedType"] != "gemini-api-key" || auth["useExternal"] != true || settings["ui"].(map[string]any)["theme"] != "custom" || security["folderTrust"].(map[string]any)["enabled"] != false {
		t.Fatalf("unrelated settings changed: %s", data)
	}
}

func TestGeminiKeySaveRejectsMalformedSettingsBeforeWritingKey(t *testing.T) {
	home := stubGeminiHome(t)
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"security":{"auth":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	_, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-new")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_STORE_INVALID" {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("key file was written: %v", err)
	}
}

func TestGeminiKeySaveRespectsEnforcedAuthType(t *testing.T) {
	home := stubGeminiHome(t)
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"security":{"auth":{"enforcedType":"oauth-personal","selectedType":"oauth-personal"}}}`)
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("gemini"), nil)
	t.Cleanup(svc.Close)
	_, err := svc.StartLogin(t.Context(), "gemini", "gemini_api_key", "sk-new")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_METHOD_UNSUPPORTED" {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("key file was written: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(settings) {
		t.Fatalf("enforced settings changed: %s, %v", got, err)
	}
}
