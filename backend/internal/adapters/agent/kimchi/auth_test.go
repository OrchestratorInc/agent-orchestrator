package kimchi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func clearKimchiAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KIMCHI_API_KEY", "")
}

func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeKimchiGlobalConfig(t *testing.T, home, content string) string {
	t.Helper()
	configPath := filepath.Join(home, ".config", "kimchi", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestConfigAuthStatusAuthorizedFromAPIKey(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{"apiKey":"test-key-123"}`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusAuthorized)
	}
}

func TestConfigAuthStatusAuthorizedFromSnakeCase(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{"api_key":"legacy-key-456"}`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusAuthorized)
	}
}

func TestConfigAuthStatusPrefersCamelCase(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{"apiKey":"new-key","api_key":"old-key"}`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusAuthorized)
	}
}

func TestConfigAuthStatusUnknownWhenNoKey(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{"otherField":"value","onboarding":{"done":true}}`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestConfigAuthStatusUnknownWhenKeyEmpty(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{"apiKey":""}`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestConfigAuthStatusUnknownWhenFileMissing(t *testing.T) {
	home := withTempHome(t)
	missingPath := filepath.Join(home, ".config", "kimchi", "config.json")

	got, err := kimchiConfigAuthStatus(context.Background(), missingPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestConfigAuthStatusUnknownWhenMalformed(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{not valid json`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestConfigAuthStatusUnknownWhenPathEmpty(t *testing.T) {
	got, err := kimchiConfigAuthStatus(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestConfigAuthStatusUnknownWhenNonStringKey(t *testing.T) {
	home := withTempHome(t)
	path := writeKimchiGlobalConfig(t, home, `{"apiKey":12345}`)

	got, err := kimchiConfigAuthStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantKey string
	}{
		{"camelCase", `{"apiKey":"abc"}`, "abc"},
		{"snake_case", `{"api_key":"def"}`, "def"},
		{"both prefers camelCase", `{"apiKey":"new","api_key":"old"}`, "new"},
		{"empty string ignored", `{"apiKey":""}`, ""},
		{"whitespace-only ignored", `{"apiKey":"  "}`, ""},
		{"missing field", `{"other":"x"}`, ""},
		{"non-string value", `{"apiKey":42}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var config map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.json), &config); err != nil {
				t.Fatal(err)
			}
			got := kimchiExtractAPIKey(config)
			if got != tc.wantKey {
				t.Fatalf("kimchiExtractAPIKey = %q, want %q", got, tc.wantKey)
			}
		})
	}
}

func TestAuthStatusAuthorizedFromEnvVar(t *testing.T) {
	clearKimchiAuthEnv(t)
	withTempHome(t)
	t.Setenv("KIMCHI_API_KEY", "env-key-789")
	plugin := &Plugin{resolvedBinary: "kimchi"}

	got, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, want %q", got, ports.AgentAuthStatusAuthorized)
	}
}

func TestAuthStatusAuthorizedFromConfig(t *testing.T) {
	clearKimchiAuthEnv(t)
	home := withTempHome(t)
	writeKimchiGlobalConfig(t, home, `{"apiKey":"file-key-012"}`)
	plugin := &Plugin{resolvedBinary: "kimchi"}

	got, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, want %q", got, ports.AgentAuthStatusAuthorized)
	}
}

func TestAuthStatusUnknownFromConfigWithoutKey(t *testing.T) {
	clearKimchiAuthEnv(t)
	home := withTempHome(t)
	writeKimchiGlobalConfig(t, home, `{"preferences":{"theme":"dark"}}`)
	plugin := &Plugin{resolvedBinary: "kimchi"}

	got, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("AuthStatus = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestAuthStatusUnknownWhenNoEnvAndNoConfig(t *testing.T) {
	clearKimchiAuthEnv(t)
	withTempHome(t)
	plugin := &Plugin{resolvedBinary: "kimchi"}

	got, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("AuthStatus = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}

func TestAuthStatusEnvVarPrecedenceOverConfig(t *testing.T) {
	clearKimchiAuthEnv(t)
	home := withTempHome(t)
	writeKimchiGlobalConfig(t, home, `{"apiKey":"config-key"}`)
	t.Setenv("KIMCHI_API_KEY", "env-key")
	plugin := &Plugin{resolvedBinary: "kimchi"}

	ctx, evidence := ports.WithAuthEvidence(context.Background())
	got, err := plugin.AuthStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, want %q", got, ports.AgentAuthStatusAuthorized)
	}
	if want := domain.EnvironmentSource("KIMCHI_API_KEY").WithMethod(domain.AgentAccountMethodAPIKey); evidence.Identity() != want {
		t.Fatalf("evidence = %+v, want %+v", evidence.Identity(), want)
	}
}

func TestAuthStatusContextCanceled(t *testing.T) {
	clearKimchiAuthEnv(t)
	plugin := &Plugin{resolvedBinary: "kimchi"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := plugin.AuthStatus(ctx)
	if err == nil {
		t.Fatal("AuthStatus: expected error from cancelled context, got nil")
	}
}

func TestAuthStatusUnknownWhenBinaryNotFound(t *testing.T) {
	clearKimchiAuthEnv(t)
	withTempHome(t)
	plugin := &Plugin{}

	got, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("AuthStatus should not return error for missing binary: %v", err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("AuthStatus = %q, want %q", got, ports.AgentAuthStatusUnknown)
	}
}
