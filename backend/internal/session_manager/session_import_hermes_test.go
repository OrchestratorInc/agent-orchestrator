package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHermesImportMarker(t *testing.T) {
	meta := domain.SessionMetadata{AgentSessionID: "native", ImportSource: &domain.SessionImportSource{NativeID: "native"}}
	if importedNativeSessionMarker(meta) != "true" {
		t.Fatal("import marker missing")
	}
	meta.AgentSessionID = "other"
	if len(importedNativeSessionMarker(meta)) != 0 {
		t.Fatal("stale import marked")
	}
	if hermesImportOwnedElsewhere(context.Background(), domain.SessionRecord{}) {
		t.Fatal("non-import owned")
	}
}
