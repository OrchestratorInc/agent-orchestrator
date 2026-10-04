package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeACPModelsPrecedeConfiguredForcedModel(t *testing.T) {
	for _, agent := range []string{"copilot", "droid"} {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("COPILOT_MODEL", "")
			root := filepath.Join(home, ".factory")
			if agent == "copilot" {
				root = filepath.Join(home, ".copilot")
			}
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"model":"forced/configured"}`), 0600); err != nil {
				t.Fatal(err)
			}
			request := ports.AgentModelDiscoveryRequest{AgentID: agent, Binary: "/should-not-execute", WorkingDir: t.TempDir()}
			d := Discoverer{ACPOptions: map[string]ACPOptionListFunc{agent: func(_ context.Context, got ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				if got.WorkingDir != request.WorkingDir || got.Binary != request.Binary {
					t.Fatal("native scope changed")
				}
				return []ports.ChatConfigOption{{ID: "model", Type: ports.ChatConfigOptionSelect, Current: ports.ChatConfigOptionValue{Select: "z/native"}, Choices: []ports.ChatConfigOptionChoice{{Value: "z/native", Name: "Native Z"}, {Value: "a/native", Name: "Native A"}}}}, nil
			}}}
			got, err := d.Discover(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, model := range got.Models {
				ids = append(ids, model.ID)
			}
			if !reflect.DeepEqual(ids, []string{"z/native", "a/native", "forced/configured"}) || got.Source != "acp" || !got.Models[0].IsDefault || got.Models[2].IsDefault {
				t.Fatalf("catalog = %#v", got)
			}
		})
	}
}

func TestNativeACPFailureFallsBackToExistingCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_MODEL", "")
	outputs := map[string]string{"copilot": "`model`: AI model to use.\n  - \"fallback-model\"\n`contextTier`: context tier.\n", "droid": "Available Models:\n  fallback-model Fallback\n\n"}
	for agent, output := range outputs {
		t.Run(agent, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "agent")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat <<'MODELS'\n"+output+"MODELS\n"), 0700); err != nil {
				t.Fatal(err)
			}
			d := Discoverer{ACPOptions: map[string]ACPOptionListFunc{agent: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				return nil, errors.New("older CLI has no ACP models")
			}}}
			got, err := d.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: agent, Binary: binary, WorkingDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Models) != 1 || got.Models[0].ID != "fallback-model" || got.Source == "acp" {
				t.Fatalf("catalog = %#v", got)
			}
		})
	}
}

func TestNativeACPConfigurationContentChangesFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for agent, folder := range map[string]string{"copilot": ".copilot", "droid": ".factory"} {
		t.Run(agent, func(t *testing.T) {
			path := filepath.Join(home, folder, "settings.json")
			writeConfig(t, path, `{"model":"same","customModels":["one"]}`)
			before := CatalogFingerprint(context.Background(), agent, "", "", nil)
			writeConfig(t, path, `{"model":"same","customModels":["two"]}`)
			if CatalogFingerprint(context.Background(), agent, "", "", nil) == before {
				t.Fatal("native config change ignored")
			}
		})
	}
}

func TestKimiNativeACPExcludesThinkingSelectors(t *testing.T) {
	options := []ports.ChatConfigOption{{ID: "model", Type: ports.ChatConfigOptionSelect, Current: ports.ChatConfigOptionValue{Select: "primary,thinking"}, Choices: []ports.ChatConfigOptionChoice{{Value: "z-plain", Name: "Z"}, {Value: "primary,thinking", Name: "Thinking"}, {Value: "a-plain", Name: "A"}}}}
	catalog, err := discoverACPOptionCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "kimi"}, func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
		return options, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 || catalog.Models[0].ID != "z-plain" || catalog.Models[1].ID != "a-plain" {
		t.Fatalf("models = %#v", catalog.Models)
	}
	options[0].Choices = []ports.ChatConfigOptionChoice{{Value: "primary,thinking"}}
	if _, err = discoverACPOptionCatalog(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "kimi"}, func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
		return options, nil
	}); err == nil {
		t.Fatal("unsupported-only native catalog accepted")
	}
}

func TestKimiNativeCredentialContentChangesFingerprint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	env := map[string]string{"KIMI_SHARE_DIR": dir}
	path := filepath.Join(dir, "credentials", "kimi-code.json")
	writeConfig(t, path, `{"access_token":"one"}`)
	before := CatalogFingerprint(context.Background(), "kimi", "", "", env)
	writeConfig(t, path, `{"access_token":"two"}`)
	if CatalogFingerprint(context.Background(), "kimi", "", "", env) == before {
		t.Fatal("credential change ignored")
	}
}

func TestGeminiNativeSettingsInvalidateCatalog(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": filepath.Join(home, "system.json"), "GEMINI_CLI_SYSTEM_DEFAULTS_PATH": filepath.Join(home, "defaults.json")}
	requestDir := t.TempDir()
	for _, path := range modelConfigPaths("gemini", requestDir, env) {
		before := CatalogFingerprint(context.Background(), "gemini", "", requestDir, env)
		writeConfig(t, path, `{"changed":"one"}`)
		if before == CatalogFingerprint(context.Background(), "gemini", "", requestDir, env) {
			t.Fatalf("change ignored: %s", path)
		}
	}
}

func TestCopilotNativeProviderTypeChangesFingerprint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_PROVIDER_TYPE", "openai")
	before := CatalogFingerprint(context.Background(), "copilot", "", "", nil)
	t.Setenv("COPILOT_PROVIDER_TYPE", "anthropic")
	if CatalogFingerprint(context.Background(), "copilot", "", "", nil) == before {
		t.Fatal("provider type change ignored")
	}
	if CatalogFingerprint(context.Background(), "copilot", "", "", map[string]string{"COPILOT_PROVIDER_TYPE": "openai"}) != before {
		t.Fatal("explicit provider type did not override process")
	}
}
