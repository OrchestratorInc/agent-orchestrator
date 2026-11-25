package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestGrokImportWithoutSource(t *testing.T) {
	if grokImportOwnedElsewhere(context.Background(), domain.SessionRecord{}) {
		t.Fatal("non-import has an owner")
	}
}

func TestGrokImportLiveRegistry(t *testing.T) {
	root := t.TempDir()
	data := fmt.Sprintf(`[{"session_id":"native","pid":%d}]`, os.Getpid())
	if err := os.WriteFile(filepath.Join(root, "active_sessions.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{}
	rec.Metadata.ImportSource = &domain.SessionImportSource{ConfigDir: root, NativeID: "native"}
	if !grokImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("live native owner not detected")
	}
	rec.Metadata.ImportSource.NativeID = "other"
	if grokImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("unrelated session treated as owner")
	}
}
