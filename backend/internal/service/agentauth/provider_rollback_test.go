package agentauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// A Continue save whose provider-key step fails must leave the earlier .env
// and config.yaml in place.
func TestContinueProviderUndoRestoresBothNativeFiles(t *testing.T) {
	global := t.TempDir()
	t.Setenv("CONTINUE_GLOBAL_DIR", global)
	envPath, configPath := filepath.Join(global, ".env"), filepath.Join(global, "config.yaml")
	envBefore := []byte("ANTHROPIC_API_KEY=old-secret\n")
	configBefore := []byte("name: Mine\nversion: 1.0.0\nschema: v1\nmodels: []\n")
	if err := os.WriteFile(envPath, envBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	undo, err := saveContinueProviderUndoable(t.Context(), "ANTHROPIC_API_KEY", "claude-new", "new-secret")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(envPath); string(got) == string(envBefore) {
		t.Fatal("save did not replace the key")
	}
	undo()
	if got, _ := os.ReadFile(envPath); string(got) != string(envBefore) {
		t.Fatalf(".env = %q, want %q", got, envBefore)
	}
	if got, _ := os.ReadFile(configPath); string(got) != string(configBefore) {
		t.Fatalf("config.yaml = %q, want %q", got, configBefore)
	}
}

// The startup republication must not publish a key removed after startup
// recorded it.
func TestStartupRepublicationSkipsKeysRemovedMeanwhile(t *testing.T) {
	previousSupported := deviceSupported
	deviceSupported = true
	t.Cleanup(func() { deviceSupported = previousSupported })
	var published []string
	previous := publishProviderKey
	publishProviderKey = func(_ context.Context, name, _, _ string) error {
		published = append(published, name)
		return nil
	}
	previousUnpublish := unpublishProviderKey
	unpublishProviderKey = func(context.Context, string, string) error { return nil }
	t.Cleanup(func() { publishProviderKey, unpublishProviderKey = previous, previousUnpublish })
	t.Setenv("OPENAI_API_KEY", "")
	t.Cleanup(func() { _ = providerkeys.RestoreInherited("OPENAI_API_KEY") })
	dir := t.TempDir()
	if err := providerkeys.Save(t.Context(), dir, "OPENAI_API_KEY", "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := providerkeys.SetDevice(t.Context(), dir, "OPENAI_API_KEY", true); err != nil {
		t.Fatal(err)
	}
	svc := &Service{dataDir: dir}
	if _, err := svc.removeProviderKey(t.Context(), "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	published = nil
	svc.republishProviderKeys(map[string]string{"OPENAI_API_KEY": "shared-secret"})
	if len(published) != 0 {
		t.Fatalf("republished removed keys: %v", published)
	}
}

// An OpenHands save whose key step fails after the model was stored must put
// the earlier model back, or later sessions pair the new model with the old key.
func TestOpenHandsModelRollsBackWhenTheKeySaveFails(t *testing.T) {
	stubDeviceEnvironment(t)
	publishProviderKey = func(context.Context, string, string, string) error { return providerkeys.ErrUserValue }
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	t.Cleanup(func() {
		_ = providerkeys.RestoreInherited("LLM_API_KEY")
		_ = providerkeys.RestoreInherited("LLM_MODEL")
	})
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("openhands"), nil, nil, dir)
	if _, err := svc.StartLoginWithModel(t.Context(), "openhands", "env_LLM_API_KEY", "old-key", "openai/gpt-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "openhands", "env_LLM_API_KEY", "new-key", LoginOptions{Model: "openai/gpt-new", Device: new(true)}); err == nil {
		t.Fatal("reported success although sharing the key failed")
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["LLM_MODEL"] != "openai/gpt-old" || keys["LLM_API_KEY"] != "old-key" {
		t.Fatalf("store after a failed save = %v, %v", keys, err)
	}
	if got := os.Getenv("LLM_MODEL"); got != "openai/gpt-old" {
		t.Fatalf("active LLM_MODEL = %q, want the earlier model", got)
	}
}

// A Qwen save whose key step fails must leave Qwen's earlier settings.json.
func TestQwenSettingsRollBackWhenTheKeySaveFails(t *testing.T) {
	stubDeviceEnvironment(t)
	publishProviderKey = func(context.Context, string, string, string) error { return providerkeys.ErrUserValue }
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Cleanup(func() { _ = providerkeys.RestoreInherited("OPENROUTER_API_KEY") })
	home := filepath.Join(t.TempDir(), "qwen")
	t.Setenv("QWEN_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, "settings.json")
	before := []byte("{\n  // mine\n  \"model\": {\"name\": \"qwen-old\"}\n}\n")
	if err := os.WriteFile(settings, before, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("qwen"), nil, nil, t.TempDir())
	if _, err := svc.StartLoginWithOptions(t.Context(), "qwen", "env_OPENROUTER_API_KEY", "sk-or-new", LoginOptions{Model: "or-new", Device: new(true)}); err == nil {
		t.Fatal("reported success although sharing the key failed")
	}
	if got, _ := os.ReadFile(settings); string(got) != string(before) {
		t.Fatalf("settings.json = %q, want the earlier settings", got)
	}
}
