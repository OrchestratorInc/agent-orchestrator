package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestParseQwenModelsUsesConfiguredProviderSelectors(t *testing.T) {
	models, err := parseQwenModels([]byte(`{
		"modelProviders": {
			"openai": [{"id":"gpt-5.6-sol","name":"GPT-5.6 Sol"}],
			"anthropic": [{"id":"claude-fable-5","name":"Fable 5"}]
		},
		"model": {"name":"gpt-5.6-sol"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol", Provider: "openai", IsDefault: true},
		{ID: "claude-fable-5", Label: "Fable 5", Provider: "anthropic"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestParseAutoHandModelsUsesConfiguredProviderModel(t *testing.T) {
	models, err := parseAutoHandModels([]byte(`{
		"provider": "zai",
		"zai": {"model": "glm-5.1"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{{
		ID: "zai/glm-5.1", Label: "glm-5.1", Provider: "zai", IsDefault: true,
	}}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestAutoHandDiscoveryReadsConfigurationWithoutRunningBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".autohand", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"provider":"zai","zai":{"model":"glm-5.1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "autohand"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "zai/glm-5.1" || got.Source != "config" {
		t.Fatalf("catalog = %#v", got)
	}
}

func TestQwenConfigDiscoveryUsesQwenHome(t *testing.T) {
	home := t.TempDir()
	qwenHome := filepath.Join(home, "custom-qwen")
	t.Setenv("HOME", home)
	t.Setenv("QWEN_HOME", qwenHome)
	if err := os.MkdirAll(qwenHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qwenHome, "settings.json"), []byte(`{
		"modelProviders":{"openai":[{"id":"gpt-5.6-sol","name":"Sol"}]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "gpt-5.6-sol" {
		t.Fatalf("catalog = %#v", got)
	}
}

func TestQwenSharedCatalogMarksConfiguredProviders(t *testing.T) {
	home := t.TempDir()
	qwenHome := filepath.Join(home, ".qwen")
	t.Setenv("HOME", home)
	t.Setenv("QWEN_HOME", "")
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "ZAI_API_KEY"} {
		t.Setenv(name, "")
	}
	if err := os.MkdirAll(qwenHome, 0o755); err != nil {
		t.Fatal(err)
	}
	// The key lives in settings.json's env map under the entry's envKey; Gemini
	// has a model entry but no key, and nothing configures Anthropic.
	if err := os.WriteFile(filepath.Join(qwenHome, "settings.json"), []byte(`{
		"model":{"name":"GLM-5.2"},
		"env":{"ZAI_API_KEY":"zai-test"},
		"modelProviders":{
			"openai":[{"id":"GLM-5.2","name":"[Z.AI] GLM-5.2","envKey":"ZAI_API_KEY"}],
			"gemini":[{"id":"gemini-3-pro","name":"Gemini 3 Pro"}]
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	request := ports.AgentModelDiscoveryRequest{AgentID: "qwen"}

	got, err := Discoverer{}.Discover(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"openai"}; !reflect.DeepEqual(got.ConfiguredProviders, want) {
		t.Fatalf("configured providers = %v, want %v", got.ConfiguredProviders, want)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models = %#v", got.Models)
	}

	// A stored Qwen OAuth login and a key in Qwen's global .env count too.
	if err := os.WriteFile(filepath.Join(qwenHome, "oauth_creds.json"), []byte(`{"access_token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qwenHome, ".env"), []byte("export GEMINI_API_KEY='gem-test'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Discoverer{}.Discover(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gemini", "openai", "qwen-oauth"}; !reflect.DeepEqual(got.ConfiguredProviders, want) {
		t.Fatalf("configured providers = %v, want %v", got.ConfiguredProviders, want)
	}
}

func TestParseContinueModels(t *testing.T) {
	models, err := parseContinueModels([]byte(`
models:
  - name: Claude Sonnet 4.6
    provider: anthropic
    model: claude-sonnet-4-6
  - name: GLM 5.2
    provider: openai
    model: glm-5.2
defaults:
  chat: Claude Sonnet 4.6
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Provider: "anthropic", IsDefault: true},
		{ID: "glm-5.2", Label: "GLM 5.2", Provider: "openai"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestParseGooseModelsReturnsOnlyActiveProviderModels(t *testing.T) {
	models, err := parseGooseModels([]byte(`
active_provider: anthropic
providers:
  anthropic:
    enabled: true
    model: claude-sonnet-4-6
    models: [claude-haiku-4-5]
  openrouter:
    enabled: true
    model: openai/gpt-5.6-sol
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "claude-sonnet-4-6", Label: "claude-sonnet-4-6", Provider: "anthropic", IsDefault: true},
		{ID: "claude-haiku-4-5", Label: "claude-haiku-4-5", Provider: "anthropic"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestParseHermesModelsReportsNativeDefault(t *testing.T) {
	levels := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	for _, tt := range []struct {
		name, config string
		want         []ports.AgentModelInfo
	}{
		{"mapping with provider and effort", "model:\n  default: anthropic/claude-opus-4.6\n  provider: anthropic\nagent:\n  reasoning_effort: high\n",
			[]ports.AgentModelInfo{{ID: "anthropic/claude-opus-4.6", Label: "anthropic/claude-opus-4.6", Provider: "anthropic", IsDefault: true, Efforts: levels, DefaultEffort: "high"}}},
		{"model key and auto provider", "model:\n  model: glm-5.3\n  provider: auto\n",
			[]ports.AgentModelInfo{{ID: "glm-5.3", Label: "glm-5.3", IsDefault: true, Efforts: levels}}},
		{"scalar model and unknown effort", "model: deepseek-chat\nagent:\n  reasoning_effort: turbo\n",
			[]ports.AgentModelInfo{{ID: "deepseek-chat", Label: "deepseek-chat", IsDefault: true, Efforts: levels}}},
		{"no model", "agent:\n  system_prompt: hi\n", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			models, err := parseHermesModels([]byte(tt.config))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(models, tt.want) {
				t.Fatalf("models = %#v, want %#v", models, tt.want)
			}
		})
	}
}

func TestHermesModelConfigFollowsHermesHome(t *testing.T) {
	home := t.TempDir()
	paths := modelConfigPaths("hermes", "", map[string]string{"HERMES_HOME": home})
	if len(paths) != 2 || paths[0] != filepath.Join(home, "config.yaml") || paths[1] != filepath.Join(home, "provider_models_cache.json") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDiscoverHermesCatalogListsConfiguredProviderModels(t *testing.T) {
	home := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.yaml", "model:\n  default: glm-5.2\n  provider: zai\n")
	write("provider_models_cache.json", `{"zai":{"models":["glm-4.6","glm-5.2"]},"anthropic":{"models":["claude-opus-5-5"]}}`)
	catalog, err := discoverConfigCatalog("hermes", "", map[string]string{"HERMES_HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, model := range catalog.Models {
		ids[model.ID] = model.IsDefault
		if model.Provider != "zai" || len(model.Efforts) == 0 {
			t.Fatalf("model %+v, want zai with reasoning levels", model)
		}
	}
	if len(ids) != 2 || !ids["glm-5.2"] || ids["glm-4.6"] {
		t.Fatalf("models = %+v, want glm-4.6 and default glm-5.2 only", catalog.Models)
	}

	// With provider auto, Hermes chooses at run time, so only the default is listed.
	write("config.yaml", "model:\n  default: glm-5.2\n  provider: auto\n")
	catalog, err = discoverConfigCatalog("hermes", "", map[string]string{"HERMES_HOME": home})
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].ID != "glm-5.2" {
		t.Fatalf("auto provider catalog = %+v, %v", catalog.Models, err)
	}
}

func TestParseVibeModelsUsesAliasesAndActiveModel(t *testing.T) {
	models, err := parseVibeModels([]byte(`
active_model = "zai-glm"
[[models]]
name = "glm-4.7"
provider = "zai"
alias = "zai-glm"
[[models]]
name = "mistral-vibe-cli-latest"
provider = "mistral"
alias = "mistral-medium-3.5"
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "zai-glm", Label: "glm-4.7", Provider: "zai", IsDefault: true},
		{ID: "mistral-medium-3.5", Label: "mistral-vibe-cli-latest", Provider: "mistral"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestParseClineModelsUsesConfiguredProviderSelections(t *testing.T) {
	models, err := parseClineModels([]byte(`{
		"lastUsedProvider":"zai-coding-plan",
		"providers": {
			"cline": {"settings":{"apiModelId":"claude-sonnet-4-6"}},
			"zai-coding-plan": {"settings":{"apiModelId":"glm-5.2"}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.AgentModelInfo{
		{ID: "glm-5.2", Label: "glm-5.2", Provider: "zai-coding-plan", IsDefault: true},
		{ID: "claude-sonnet-4-6", Label: "claude-sonnet-4-6", Provider: "cline"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestConfigCatalogDiscoveryAndFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".continue", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models:\n  - name: Sonnet\n    provider: anthropic\n    model: claude-sonnet-4-6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := CatalogFingerprint(context.Background(), "continue", "", "", nil)
	got, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "claude-sonnet-4-6" || got.SelectionMode != ports.ModelSelectionCatalog {
		t.Fatalf("catalog = %#v", got)
	}
	if err := os.WriteFile(path, []byte("models:\n  - name: Opus\n    provider: anthropic\n    model: claude-opus-5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := CatalogFingerprint(context.Background(), "continue", "", "", nil)
	if before == after {
		t.Fatalf("fingerprint did not change after config edit: %q", before)
	}
}
