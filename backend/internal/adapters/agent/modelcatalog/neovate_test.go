package modelcatalog

import "testing"

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
