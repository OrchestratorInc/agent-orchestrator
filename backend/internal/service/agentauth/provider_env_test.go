package agentauth

import (
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func TestEverySupportedHarnessHasAccountSetupMethod(t *testing.T) {
	for _, harness := range domain.AllHarnesses {
		if methods := loginMethods(string(harness)); len(methods) == 0 {
			t.Errorf("%s has no Account setup method", harness)
		}
	}
}

func TestEnvironmentMethodIsHarnessSpecific(t *testing.T) {
	if _, ok := environmentMethod("goose", "env_OPENAI_API_KEY"); !ok {
		t.Fatal("Goose lost its OpenAI provider method")
	}
	if _, ok := environmentMethod("grok", "env_OPENAI_API_KEY"); ok {
		t.Fatal("offered Grok an unrelated provider key")
	}
	if _, ok := environmentMethod("unknown", "env_OPENAI_API_KEY"); ok {
		t.Fatal("unknown harness accepted a provider key")
	}
}

func TestEnvironmentLoginPersistsWithoutStartingCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "")
	svc := NewWithAgentResolver(foundExecutable("goose"), nil, nil, dir)
	login, err := svc.StartLogin(t.Context(), "goose", "env_OPENAI_API_KEY", "test-secret")
	if err != nil || login.Status != "succeeded" {
		t.Fatalf("StartLogin = %+v, %v", login, err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["OPENAI_API_KEY"] != "test-secret" || os.Getenv("OPENAI_API_KEY") != "test-secret" {
		t.Fatalf("saved key missing: %v, %v", keys, err)
	}
	if _, err := svc.StartLogin(t.Context(), "goose", "env_FAKE_API_KEY", "test-secret"); err == nil {
		t.Fatal("accepted unreviewed environment variable")
	}
	if _, err := svc.StartLogin(t.Context(), "goose", "env_OPENAI_API_KEY", "line\nbreak"); err == nil {
		t.Fatal("accepted multiline credential")
	}
}
