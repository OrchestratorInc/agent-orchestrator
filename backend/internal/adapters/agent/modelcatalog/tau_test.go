package modelcatalog

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTauCatalogKeepsExactProviderModelSelection(t *testing.T) {
	output := []byte("*\topenrouter\topenai\tvendor/model\tvendor/model,other\tKEY\tenv:KEY\thttps://example.invalid\t60s\tretries=2\tretry_delay=1s\n \tother-provider\topenai\tvendor/model\tvendor/model\tOTHER\tmissing\t\t60s\tretries=2\tretry_delay=1s\n")
	models, err := parseTauModels(output)
	if err != nil || len(models) != 3 {
		t.Fatalf("models = %#v, %v", models, err)
	}
	if models[0].ID != "openrouter/vendor/model" || !models[0].IsDefault || models[2].ID != "other-provider/vendor/model" || models[2].IsDefault {
		t.Fatalf("ambiguous model selection: %#v", models)
	}
	if _, err := parseTauModels([]byte("malformed")); err == nil {
		t.Fatal("invalid provider output became a successful empty catalog")
	}
	catalog := Base("tau")
	if catalog.CustomModelEntry != ports.CustomModelEntryConfigured || catalog.AllowCustom {
		t.Fatalf("unsupported arbitrary model selection: %#v", catalog)
	}
}
