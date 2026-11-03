package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAutohandImportIgnoresStaleRegistry(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "active-agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-agents", "stale.json"), []byte(`{"sessionId":"synthetic-id","pid":0,"workspaceRoot":"/elsewhere"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{Harness: domain.HarnessAutohand, Metadata: domain.SessionMetadata{
		ImportSource: &domain.SessionImportSource{NativeID: "synthetic-id", ConfigDir: root},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if autohandImportOwnedElsewhere(ctx, rec) {
		t.Fatal("canceled probe reported owner")
	}
	if autohandImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("stale pid reported owner")
	}
	if hooks := importedHarnesses[domain.HarnessAutohand]; hooks.configEnvKey != "AUTOHAND_HOME" || hooks.adoptedOwnedElsewhere == nil {
		t.Fatal("autohand import hooks missing")
	}
}
