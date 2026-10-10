package modelcatalog

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMiniMaxConfiguredModels(t *testing.T) {
	data := []byte(`{"providers":[{"providerId":"custom_provider:zai","enabled":true,"hasApiKey":true,"models":[{"modelId":"glm-5.3-flash","selected":true}]},{"providerId":"minimax_oauth","enabled":true,"models":[]}]}`)
	got, err := parseMiniMaxModels(data)
	if err != nil || len(got) != 1 || got[0].ID != "custom_provider:zai/glm-5.3-flash" || !got[0].IsDefault {
		t.Fatalf("models=%v, %v", got, err)
	}
	if Base("minimax-code").CustomModelEntry != ports.CustomModelEntryConfigured {
		t.Fatal("MiniMax must use configured model IDs")
	}
}
