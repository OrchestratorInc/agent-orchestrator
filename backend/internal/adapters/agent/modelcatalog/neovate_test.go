package modelcatalog

import (
	"strings"
	"testing"
)

func TestNeovateModelsComeOnlyFromNativeConfiguration(t *testing.T) {
	models, err := parseNeovateModels([]byte(`{"model":"anthropic/claude-opus-4-6","provider":{"local":{"options":{"apiKey":"secret"},"models":{"my-model":{}}}}}`))
	if err != nil || len(models) != 2 {
		t.Fatalf("models = %#v, %v", models, err)
	}
	for _, model := range models {
		if model.ID != "anthropic/claude-opus-4-6" && model.ID != "local/my-model" {
			t.Fatalf("unexpected model %#v", model)
		}
	}
	if got := Base("neovate"); got.AllowCustom || got.CustomModelEntry != "configured" {
		t.Fatalf("model policy = %#v", got)
	}
}

func TestNeovateOffersAllBuiltInModelsDefaultingToAKeyedProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, provider := range neovateBuiltInProviders {
		for _, name := range provider.env {
			t.Setenv(name, "")
		}
	}
	unkeyed, err := discoverConfigCatalog("neovate", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unkeyed.Models) != len(neovateAllModels()) {
		t.Fatalf("models = %d, want every built-in model (%d)", len(unkeyed.Models), len(neovateAllModels()))
	}
	for _, model := range unkeyed.Models {
		if model.IsDefault {
			t.Fatalf("unkeyed Neovate defaulted to %#v", model)
		}
	}
	keyed, err := discoverConfigCatalog("neovate", t.TempDir(), map[string]string{"ZHIPU_API_KEY": "test"})
	if err != nil {
		t.Fatal(err)
	}
	var defaults []string
	for _, model := range keyed.Models {
		if model.IsDefault {
			defaults = append(defaults, model.ID)
		}
	}
	if len(defaults) != 1 || !strings.HasPrefix(defaults[0], "zai-coding-plan/") {
		t.Fatalf("defaults = %v, want one model of the first keyed provider", defaults)
	}
}
