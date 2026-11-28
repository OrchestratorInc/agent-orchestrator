package sqlitetest_test

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestOpenReturnsCurrentIsolatedStores(t *testing.T) {
	first := sqlitetest.MustOpen(t)
	if err := first.UpsertProject(context.Background(), domain.ProjectRecord{
		ID:           "first",
		Path:         "/tmp/first",
		RegisteredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed first store: %v", err)
	}

	second := sqlitetest.MustOpen(t)
	if _, found, err := second.GetProject(context.Background(), "first"); err != nil {
		t.Fatalf("read second store: %v", err)
	} else if found {
		t.Fatal("second store inherited rows from first store")
	}
}

func TestOpenRefusesToReplaceExistingDatabase(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	if _, err := sqlitetest.Open(dataDir); err == nil {
		t.Fatal("second open replaced an existing database")
	}
}
