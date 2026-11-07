package crush

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
		"GOOGLE_APPLICATION_CREDENTIALS", "XDG_CONFIG_HOME", "APPDATA",
	} {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	status, ok, err := crushLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}
