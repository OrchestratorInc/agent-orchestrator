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
	if err := svc.removeProviderKey(t.Context(), "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	published = nil
	svc.republishProviderKeys(map[string]string{"OPENAI_API_KEY": "shared-secret"})
	if len(published) != 0 {
		t.Fatalf("republished removed keys: %v", published)
	}
}
