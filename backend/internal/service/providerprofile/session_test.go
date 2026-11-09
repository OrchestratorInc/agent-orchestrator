package providerprofile

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestUnboundNativeCapabilityRequestPreservesSelection 删除无绑定分支会抹掉已有原生请求的模型与 effort。
func TestUnboundNativeCapabilityRequestPreservesSelection(t *testing.T) {
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	request := ports.CapabilityProbeRequest{SessionID: "native-session", Harness: domain.HarnessCodex, Model: "native-model", Effort: "high"}
	got, err := ScopeCapabilityRequest(context.Background(), store, request)
	if err != nil || got != request {
		t.Fatalf("native selection changed: %+v, %v", got, err)
	}
	request.Profile = "unbound-profile"
	if _, err := ScopeCapabilityRequest(context.Background(), store, request); err == nil {
		t.Fatal("unbound request introduced a provider profile")
	}
}
