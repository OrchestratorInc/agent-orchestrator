package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMiMoCodeImportDatabaseBinding(t *testing.T) {
	if got := mimoCodeImportDatabase("/custom/data"); got != filepath.Join("/custom/data", "mimocode.db") {
		t.Fatal(got)
	}
	if mimoCodeImportOwnedElsewhere(context.Background(), domain.SessionRecord{}) {
		t.Fatal("global database is not per-session ownership evidence")
	}
}
