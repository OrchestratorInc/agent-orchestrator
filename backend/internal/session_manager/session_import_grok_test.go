package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestGrokImportWithoutSource(t *testing.T) {
	if grokImportOwnedElsewhere(context.Background(), domain.SessionRecord{}) {
		t.Fatal("non-import has an owner")
	}
}
