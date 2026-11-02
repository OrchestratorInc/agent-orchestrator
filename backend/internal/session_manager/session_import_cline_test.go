package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClineImportIgnoresStaleManifest(t *testing.T) {
	root := t.TempDir()
	id := "1790000000000_synthetic"
	dir := filepath.Join(root, "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"session_id":"`+id+`","pid":0,"status":"completed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{Harness: domain.HarnessCline, Metadata: domain.SessionMetadata{
		ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: root},
	}}
	if clineImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("stale manifest reported owner")
	}
	if hooks := importedHarnesses[domain.HarnessCline]; hooks.configEnvKey != "CLINE_DATA_DIR" || hooks.adoptedOwnedElsewhere == nil {
		t.Fatal("cline import hooks missing")
	}
}
