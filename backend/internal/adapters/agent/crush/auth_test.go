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
	// Clear every env credential crushLocalAuthStatus accepts: the test must
	// hold on any machine whose shell exports an unrelated provider key.
	for _, name := range []string{
		"HYPER_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "VERCEL_API_KEY",
		"GEMINI_API_KEY", "ZAI_API_KEY", "MINIMAX_API_KEY", "SYNTHETIC_API_KEY",
		"HF_TOKEN", "CEREBRAS_API_KEY", "OPENROUTER_API_KEY", "IONET_API_KEY",
		"ALIBABA_SINGAPORE_API_KEY", "ALIBABA_US_API_KEY", "GROQ_API_KEY",
		"AVIAN_API_KEY", "OPENCODE_API_KEY", "AZURE_OPENAI_API_KEY", "MOONSHOT_API_KEY",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_PROFILE", "VERTEXAI_PROJECT", "VERTEXAI_LOCATION",
		"GOOGLE_APPLICATION_CREDENTIALS", "XDG_CONFIG_HOME",
	} {
		t.Setenv(name, "")
	}
	status, ok, err := crushLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}
