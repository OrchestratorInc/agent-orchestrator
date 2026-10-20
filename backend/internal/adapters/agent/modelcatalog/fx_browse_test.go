package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFXCatalogKeepsDuplicateModelsSelectableByConnection(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".fx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fx", "settings.json"), []byte(`{"models":{"grok":"grok-4"},"providers":{"local":{"protocol":"openai-chat-completions","auth":{"type":"none"},"model_metadata":{"local-model":{"context_window":32768}}},"bad/name":{}}}`), 0o600); err != nil {
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
	got, err := discoverFXCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Binary: "fx", Env: env}, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Providers, []string{"gateway", "codex", "grok", "local"}) || !reflect.DeepEqual(got.ConfiguredProviders, []string{"codex"}) {
		t.Fatalf("providers = %v, configured = %v", got.Providers, got.ConfiguredProviders)
	}
	count := 0
	for _, model := range got.Models {
		if model.ID == "fx:gateway:openai/shared" || model.ID == "fx:codex:openai/shared" {
			count++
		}
		if model.ID == "fx:local:local-model" && (model.Provider != "local" || model.ContextWindow != 32768) {
			t.Fatalf("custom metadata = %#v", model)
		}
	}
	if count != 2 || len(got.Models) != 4 || env["FX_PROVIDER"] != "codex" {
		t.Fatalf("connection choices or caller environment incorrect: %#v, %v", got.Models, env)
	}
	if strings.Contains(got.Warning, "secret") || !strings.Contains(got.Warning, "grok") {
		t.Fatalf("unsafe or missing failure explanation: %s", got.Warning)
	}
}

func TestFXCatalogDoesNotWarnForConnectionsNeverSetUp(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".fx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fx", "settings.json"), []byte(`{"provider":"gateway","models":{"codex":"gpt-5.6-terra","gateway":"openai/gpt-5.2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(_ context.Context, _ string, args []string, _ string, childEnv map[string]string) ([]byte, error) {
		if strings.Join(args, " ") == "status --json" {
			return []byte(`{"kind":"status","auth":"fx login"}`), nil
		}
		switch childEnv["FX_PROVIDER"] {
		case "gateway":
			return []byte(`{"ids":["openai/gpt-5.2"]}`), nil
		case "codex":
			return nil, errors.New("exit status 1")
		}
		return []byte("fx: no Grok model is selected"), errors.New("exit status 1")
	}
	got, err := discoverFXCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Binary: "fx", Env: map[string]string{"HOME": home}}, run)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["unavailableProviders"] != "codex" || !strings.Contains(got.Warning, "codex") || strings.Contains(got.Warning, "grok") {
		t.Fatalf("warning = %q, metadata = %v", got.Warning, got.Metadata)
	}
}

func TestFXCatalogLegacySavedModels(t *testing.T) {
	for _, tc := range []struct {
		name        string
		settings    string
		unavailable string
	}{
		{"codex", `{"provider":"gateway","codex_model":"gpt-5.4-mini"}`, "codex"},
		{"grok", `{"provider":"gateway","grok_model":"grok-4"}`, "grok"},
		{"both", `{"codex_model":"gpt-5.4-mini","grok_model":"grok-4"}`, "codex,grok"},
		{"blank legacy models", `{"codex_model":" ","grok_model":""}`, ""},
		{"new map overrides legacy", `{"codex_model":"gpt-5.4-mini","grok_model":"grok-4","models":{"codex":"","grok":" "}}`, ""},
		{"new map preserves other legacy providers", `{"codex_model":"gpt-5.4-mini","grok_model":"grok-4","models":{"codex":""}}`, "grok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, statusFails := range []bool{false, true} {
				name := "status without connections"
				if statusFails {
					name = "status fails"
				}
				t.Run(name, func(t *testing.T) {
					home := t.TempDir()
					if err := os.MkdirAll(filepath.Join(home, ".fx"), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(home, ".fx", "settings.json"), []byte(tc.settings), 0o600); err != nil {
						t.Fatal(err)
					}
					run := func(_ context.Context, _ string, args []string, _ string, env map[string]string) ([]byte, error) {
						if strings.Join(args, " ") == "status --json" && !statusFails {
							return []byte(`{"kind":"status"}`), nil
						}
						if strings.Join(args, " ") == "models --json" && env["FX_PROVIDER"] == "gateway" {
							return []byte(`{"ids":["openai/gpt-5.2"]}`), nil
						}
						return nil, errors.New("offline")
					}
					got, err := discoverFXCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Binary: "fx", Env: map[string]string{"HOME": home}}, run)
					if err != nil {
						t.Fatal(err)
					}
					if got.Metadata["unavailableProviders"] != tc.unavailable {
						t.Fatalf("unavailable providers = %q, want %q", got.Metadata["unavailableProviders"], tc.unavailable)
					}
					for _, provider := range []string{"codex", "grok"} {
						if strings.Contains(got.Warning, provider) != strings.Contains(tc.unavailable, provider) {
							t.Fatalf("warning = %q, want unavailable providers %q", got.Warning, tc.unavailable)
						}
					}
				})
			}
		})
	}
}

func TestFXCatalogRetainsProviderListWithoutLogin(t *testing.T) {
	run := func(_ context.Context, _ string, _ []string, _ string, _ map[string]string) ([]byte, error) {
		return nil, errors.New("offline")
	}
	got, err := discoverFXCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Binary: "fx", Env: map[string]string{"HOME": t.TempDir()}}, run)
	if err != nil || len(got.Providers) != 3 || len(got.ConfiguredProviders) != 0 || len(got.Models) != 0 || got.Warning == "" {
		t.Fatalf("catalog = %#v, err = %v", got, err)
	}
}

func TestSharedDiscoveryIncludesEveryFXConnection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a shell script")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "fx")
	script := `#!/bin/sh
case "$1" in
status) printf '%s' '{"connected_providers":["codex"]}' ;;
models) printf '%s' '{"ids":["shared-model"]}' ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "fx", Binary: binary, Env: map[string]string{"HOME": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
	}
	want := []string{"fx:codex:shared-model", "fx:gateway:shared-model", "fx:grok:shared-model"}
	if !reflect.DeepEqual(ids, want) || !reflect.DeepEqual(catalog.ConfiguredProviders, []string{"codex"}) {
		t.Fatalf("shared catalog = %#v", catalog)
	}
}
