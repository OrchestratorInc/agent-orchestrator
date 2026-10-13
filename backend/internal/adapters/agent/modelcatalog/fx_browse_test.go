package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFXBrowsingIncludesDisconnectedProvidersAndNativeIDs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".fx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fx", "settings.json"), []byte(`{"providers":{"local":{"protocol":"openai-chat-completions","auth":{"type":"none"},"model_metadata":{"local-model":{"context_window":32768}}},"bad/name":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "FX_PROVIDER": "codex"}
	run := func(_ context.Context, _ string, args []string, _ string, childEnv map[string]string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "status --json":
			return []byte(`{"connected_providers":["codex"],"auth":"Codex subscription"}`), nil
		case "models --json":
			switch childEnv["FX_PROVIDER"] {
			case "gateway":
				return []byte(`{"ids":["openai/shared","anthropic/claude"]}`), nil
			case "codex":
				return []byte(`{"ids":["openai/shared"]}`), nil
			case "grok":
				return nil, errors.New("secret raw provider error")
			}
		}
		t.Errorf("unexpected command %v for %s", args, childEnv["FX_PROVIDER"])
		return nil, errors.New("unexpected command")
	}
	got, err := discoverFXBrowsingCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Binary: "fx", Env: env}, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Providers, []string{"gateway", "codex", "grok", "local"}) || !reflect.DeepEqual(got.ConfiguredProviders, []string{"codex"}) {
		t.Fatalf("providers = %v, configured = %v", got.Providers, got.ConfiguredProviders)
	}
	count := 0
	for _, model := range got.Models {
		if model.ID == "openai/shared" {
			count++
		}
		if model.ID == "local-model" && (model.Provider != "local" || model.ContextWindow != 32768) {
			t.Fatalf("custom metadata = %#v", model)
		}
	}
	if count != 2 || len(got.Models) != 4 || env["FX_PROVIDER"] != "codex" {
		t.Fatalf("native IDs or caller environment changed: %#v, %v", got.Models, env)
	}
	if strings.Contains(got.Warning, "secret") || !strings.Contains(got.Warning, "grok") {
		t.Fatalf("unsafe or missing failure explanation: %s", got.Warning)
	}
}

func TestFXBrowsingRetainsProviderListWithoutLogin(t *testing.T) {
	run := func(_ context.Context, _ string, _ []string, _ string, _ map[string]string) ([]byte, error) {
		return nil, errors.New("offline")
	}
	got, err := discoverFXBrowsingCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Binary: "fx", Env: map[string]string{"HOME": t.TempDir()}}, run)
	if err != nil || len(got.Providers) != 3 || len(got.ConfiguredProviders) != 0 || len(got.Models) != 0 || got.Warning == "" {
		t.Fatalf("catalog = %#v, err = %v", got, err)
	}
}
