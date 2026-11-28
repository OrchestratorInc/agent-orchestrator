package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestKiloCodeImportBinding(t *testing.T) {
	if got := kiloCodeImportDataHome(filepath.Join("/custom/data", "kilo")); got != filepath.Clean("/custom/data") {
		t.Fatal(got)
	}
	if kiloCodeImportOwnedElsewhere(context.Background(), domain.SessionRecord{}) {
		t.Fatal("global database is not per-session ownership evidence")
	}
}
