package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHermesImportMarker(t *testing.T) {
	meta := domain.SessionMetadata{AgentSessionID: "native", ImportSource: &domain.SessionImportSource{NativeID: "native"}}
	if importedNativeSessionMarker(meta) != "true" {
		t.Fatal("import marker missing")
	}
	meta.AgentSessionID = "other"
	if importedNativeSessionMarker(meta) != "" {
		t.Fatal("stale import marked")
	}
	if hermesImportOwnedElsewhere(context.Background(), domain.SessionRecord{}) {
		t.Fatal("non-import owned")
	}
}

func TestHermesImportLiveRegistry(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"entries":[{"session_id":"native","pid":%d}]}`, os.Getpid())
	if err := os.WriteFile(filepath.Join(root, "runtime", "active_sessions.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{}
	rec.Metadata.ImportSource = &domain.SessionImportSource{ConfigDir: root, NativeID: "native"}
	if !hermesImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("live owner not detected")
	}
	rec.Metadata.ImportSource.NativeID = "unrelated"
	if hermesImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("unrelated owner detected")
	}
}
