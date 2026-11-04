package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDevinImportWithoutWriter(t *testing.T) {
	rec := domain.SessionRecord{}
	if devinImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("non-import has owner")
	}
	if got := devinImportDataHome(filepath.Join("/data", "devin", "cli")); got != filepath.Clean("/data") {
		t.Fatal(got)
	}
}
