package crush

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestCrushLocalAuthStatusAuthorizedWithDocumentedEnv(t *testing.T) {
	t.Setenv("HYPER_API_KEY", "test-key")
	status, ok, err := crushLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func TestCrushLocalAuthStatusDoesNotUseProviderCatalog(t *testing.T) {
	for _, name := range crushAPIKeyEnvVars {
		t.Setenv(name, "")
	}
	for _, name := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_PROFILE",
		"VERTEXAI_PROJECT", "VERTEXAI_LOCATION",
		"GOOGLE_APPLICATION_CREDENTIALS", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA",
		"CRUSH_GLOBAL_CONFIG", "CRUSH_GLOBAL_DATA",
	} {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// A downloaded provider catalog, including api_key templates, is metadata
	// and must not count as evidence that the user is authenticated.
	catalog := filepath.Join(home, ".local", "share", "crush", "providers.json")
	if err := os.MkdirAll(filepath.Dir(catalog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog, []byte(`[{"id":"openai","type":"openai","api_key":"$OPENAI_API_KEY"},{"id":"anthropic","type":"anthropic","api_key":"$ANTHROPIC_API_KEY"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, ok, err := crushLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}
