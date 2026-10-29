package agentauth

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func TestEverySupportedHarnessHasAccountSetupMethod(t *testing.T) {
	// These CLIs authenticate through their own account flow and have no
	// documented provider-key environment source for their runtime.
	accountOnly := map[domain.AgentHarness]bool{
		domain.HarnessAgy: true, domain.HarnessAuggie: true, domain.HarnessMiMoCode: true,
	}
	for _, harness := range domain.AllHarnesses {
		methods := loginMethods(string(harness))
		if len(methods) == 0 {
			t.Errorf("%s has no Account setup method", harness)
		}
		if accountOnly[harness] {
			continue
		}
		var hasKey bool
		for _, method := range methods {
			hasKey = hasKey || method.Secret
		}
		if !hasKey {
			t.Errorf("%s has no provider-key method", harness)
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
	previous := publishProviderKey
	publishProviderKey = func(_ context.Context, name, _ string) error {
		t.Fatalf("published %s to the device environment without the user choosing it", name)
		return nil
	}
	t.Cleanup(func() { publishProviderKey = previous })
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

func TestEnvironmentLoginReportsDevicePublicationFailure(t *testing.T) {
	previous := publishProviderKey
	publishProviderKey = func(context.Context, string, string) error { return errors.New("device unavailable") }
	t.Cleanup(func() { publishProviderKey = previous })
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("goose"), nil, nil, dir)
	if _, err := svc.StartLoginWithOptions(t.Context(), "goose", "env_OPENAI_API_KEY", "test-secret", LoginOptions{Device: true}); err == nil {
		t.Fatal("reported success without publishing to the device environment")
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || len(keys) != 0 {
		t.Fatalf("AO stored a key after device publication failed: %v, %v", keys, err)
	}
}

func TestOpenHandsProviderPublishesKeyAndModel(t *testing.T) {
	previous := publishProviderKey
	published := map[string]string{}
	publishProviderKey = func(_ context.Context, name, value string) error {
		published[name] = value
		return nil
	}
	t.Cleanup(func() { publishProviderKey = previous })
	t.Setenv("LLM_MODEL", "")
	t.Setenv("LLM_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("openhands"), nil, nil, dir)
	if _, err := svc.StartLogin(t.Context(), "openhands", "env_LLM_API_KEY", "test-secret"); err == nil {
		t.Fatal("accepted OpenHands key without model")
	}
	result, err := svc.StartLoginWithOptions(t.Context(), "openhands", "env_LLM_API_KEY", "test-secret", LoginOptions{Model: "anthropic/claude-sonnet-4-5", Device: true})
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("OpenHands login = %+v, %v", result, err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["LLM_API_KEY"] != "test-secret" || keys["LLM_MODEL"] != "anthropic/claude-sonnet-4-5" ||
		published["LLM_API_KEY"] != "test-secret" || published["LLM_MODEL"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("OpenHands config = %v, published %v, %v", keys, published, err)
	}
}
