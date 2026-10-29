package agentauth

import (
	"context"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func stubDeviceEnvironment(t *testing.T) (published, withdrawn map[string]string) {
	t.Helper()
	published, withdrawn = map[string]string{}, map[string]string{}
	previousPublish, previousUnpublish := publishProviderKey, unpublishProviderKey
	publishProviderKey = func(_ context.Context, name, value string) error {
		published[name] = value
		return nil
	}
	unpublishProviderKey = func(_ context.Context, name string) error {
		withdrawn[name] = ""
		return nil
	}
	t.Cleanup(func() { publishProviderKey, unpublishProviderKey = previousPublish, previousUnpublish })
	return published, withdrawn
}

func TestProviderKeysListSharedProvidersWithoutValues(t *testing.T) {
	stubDeviceEnvironment(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	if _, err := svc.SaveProviderKey(t.Context(), "ANTHROPIC_API_KEY", "sk-ant-test-secret-1234", false); err != nil {
		t.Fatal(err)
	}
	keys, err := svc.ProviderKeys(t.Context())
	if err != nil || len(keys) == 0 {
		t.Fatalf("ProviderKeys = %v, %v", keys, err)
	}
	if len(keys[0].Agents) < len(keys[len(keys)-1].Agents) {
		t.Fatal("shared provider keys are not listed first")
	}
	for _, key := range keys {
		if key.Name != "ANTHROPIC_API_KEY" {
			continue
		}
		if !key.Configured || key.Hint != "1234" || key.Device || !key.Checkable || len(key.Agents) < 2 {
			t.Fatalf("Anthropic key = %+v", key)
		}
		return
	}
	t.Fatal("Anthropic key missing")
}

func TestSaveProviderKeyPublishesOnlyWhenChosen(t *testing.T) {
	published, withdrawn := stubDeviceEnvironment(t)
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	if _, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "openai-secret", false); err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 || len(withdrawn) != 0 {
		t.Fatalf("touched device environment: published %v, withdrawn %v", published, withdrawn)
	}
	// An empty secret keeps the saved key and only changes the device setting.
	key, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "", true)
	if err != nil || !key.Device || published["OPENAI_API_KEY"] != "openai-secret" {
		t.Fatalf("publish = %+v, %v, %v", key, published, err)
	}
	key, err = svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "", false)
	if _, ok := withdrawn["OPENAI_API_KEY"]; err != nil || key.Device || !ok {
		t.Fatalf("withdraw = %+v, %v, %v", key, withdrawn, err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "PATH", "value", false); err == nil {
		t.Fatal("accepted an unreviewed variable")
	}
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "", false); err == nil {
		t.Fatal("saved an empty key")
	}
}

func TestRemoveProviderKeyWithdrawsOnlyWhatAOPublished(t *testing.T) {
	_, withdrawn := stubDeviceEnvironment(t)
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("XAI_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("openhands"), nil, nil, dir)
	if _, err := svc.StartLoginWithOptions(t.Context(), "openhands", "env_LLM_API_KEY", "test-secret", LoginOptions{Model: "openai/gpt-5", Device: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "XAI_API_KEY", "xai-secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveProviderKey(t.Context(), "LLM_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveProviderKey(t.Context(), "XAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, ok := withdrawn["LLM_MODEL"]; !ok {
		t.Fatalf("companion model stayed in the device environment: %v", withdrawn)
	}
	if _, ok := withdrawn["XAI_API_KEY"]; ok {
		t.Fatal("withdrew a variable AO never published")
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || len(keys) != 0 || os.Getenv("LLM_API_KEY") != "" || os.Getenv("LLM_MODEL") != "" {
		t.Fatalf("keys after removal = %v, %v", keys, err)
	}
}

func TestEnvironmentLoginReusesSavedKey(t *testing.T) {
	stubDeviceEnvironment(t)
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("openhands"), nil, nil, dir)
	if _, err := svc.StartLoginWithModel(t.Context(), "openhands", "env_LLM_API_KEY", "", "openai/gpt-5"); err == nil {
		t.Fatal("accepted an empty key with nothing saved")
	}
	if _, err := svc.SaveProviderKey(t.Context(), "LLM_API_KEY", "saved-secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLoginWithModel(t.Context(), "openhands", "env_LLM_API_KEY", "", "anthropic/claude-sonnet-4-5"); err != nil {
		t.Fatal(err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["LLM_API_KEY"] != "saved-secret" || keys["LLM_MODEL"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "codex", "browser", "", LoginOptions{Device: true}); err == nil {
		t.Fatal("accepted a device setting for a native login")
	}
}
