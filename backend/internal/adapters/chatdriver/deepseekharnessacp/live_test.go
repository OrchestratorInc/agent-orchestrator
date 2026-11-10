package deepseekharnessacp

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// TestLiveDeepSeekACPModelCatalog 以无 prompt 的独立会话验证真实模型目录与原生思考档位。
// AO_LIVE_DEEPSEEK=1 go test ./internal/adapters/chatdriver/deepseekharnessacp -run Live -v
func TestLiveDeepSeekACPModelCatalog(t *testing.T) {
	if os.Getenv("AO_LIVE_DEEPSEEK") != "1" {
		t.Skip("set AO_LIVE_DEEPSEEK=1 to test the installed DeepSeek Harness")
	}
	binary, err := exec.LookPath("dsh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	models, err := acpdriver.DiscoverModels(ctx, acpdriver.Launch{
		Command: binary, Args: []string{"--profile", acpProfile},
	}, t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("ACP returned no models")
	}
	withEfforts := 0
	for _, model := range models {
		if len(model.ID) < 2 || model.ID[0] != '[' || model.ID[len(model.ID)-1] != ']' {
			t.Fatalf("model ID is not an opaque JSON array: %q", model.ID)
		}
		if len(model.Efforts) > 0 {
			withEfforts++
		}
		t.Logf("model=%s efforts=%v default=%s", model.ID, model.Efforts, model.DefaultEffort)
	}
	if withEfforts == 0 {
		t.Fatal("ACP discovery lost every model's reasoning choices")
	}
}
