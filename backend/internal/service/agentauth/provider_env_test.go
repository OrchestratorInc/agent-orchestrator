package agentauth

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/providerkeys"
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
	publishProviderKey = func(_ context.Context, name, _, _ string) error {
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
	publishProviderKey = func(context.Context, string, string, string) error { return errors.New("device unavailable") }
	t.Cleanup(func() { publishProviderKey = previous })
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("goose"), nil, nil, dir)
	if _, err := svc.StartLoginWithOptions(t.Context(), "goose", "env_OPENAI_API_KEY", "test-secret", LoginOptions{Device: new(true)}); err == nil {
		t.Fatal("reported success without publishing to the device environment")
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || len(keys) != 0 {
		t.Fatalf("AO stored a key after device publication failed: %v, %v", keys, err)
	}
}

func TestOpenHandsProviderPublishesKeyAndModel(t *testing.T) {
	allowDeviceEnvironment(t)
	previous := publishProviderKey
	published := map[string]string{}
	publishProviderKey = func(_ context.Context, name, value, _ string) error {
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
	result, err := svc.StartLoginWithOptions(t.Context(), "openhands", "env_LLM_API_KEY", "test-secret", LoginOptions{Model: "anthropic/claude-sonnet-4-5", Device: new(true)})
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("OpenHands login = %+v, %v", result, err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["LLM_API_KEY"] != "test-secret" || keys["LLM_MODEL"] != "anthropic/claude-sonnet-4-5" ||
		published["LLM_API_KEY"] != "test-secret" || published["LLM_MODEL"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("OpenHands config = %v, published %v, %v", keys, published, err)
	}
}

// Mastra Code's google provider reads GOOGLE_API_KEY first, so its saved
// Google key must use that name: injected over the session environment, it
// beats an inherited GOOGLE_API_KEY, and without a conflict it is still set.
func TestMastraCodeGoogleKeyIsTheNameMastraReadsFirst(t *testing.T) {
	name, ok := environmentMethod("mastra-code", "env_GOOGLE_API_KEY")
	if !ok || name != "GOOGLE_API_KEY" {
		t.Fatalf("Mastra Code Google method = %q, %v; want GOOGLE_API_KEY", name, ok)
	}
	if _, ok := environmentMethod("mastra-code", "env_GOOGLE_GENERATIVE_AI_API_KEY"); ok {
		t.Fatal("Mastra Code still saves the Google key under a name it reads second")
	}
	saved := map[string]string{name: "ao-saved"}
	for _, inherited := range [][]string{{"PATH=/bin", "GOOGLE_API_KEY=inherited"}, {"PATH=/bin"}} {
		var got []string
		for _, entry := range providerkeys.EnvList(inherited, saved) {
			if len(entry) > len("GOOGLE_API_KEY=") && entry[:len("GOOGLE_API_KEY=")] == "GOOGLE_API_KEY=" {
				got = append(got, entry)
			}
		}
		if len(got) != 1 || got[0] != "GOOGLE_API_KEY=ao-saved" {
			t.Fatalf("session GOOGLE_API_KEY from %v = %v; want only the AO-saved key", inherited, got)
		}
	}
}
