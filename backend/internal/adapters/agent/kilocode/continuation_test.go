package kilocode

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestKiloCodeNativeImportProbe(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "kilo.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, time_archived INTEGER); INSERT INTO session VALUES ('ses_synthetic', NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	p := New()
	ref := ports.NativeSessionRef{ConfigDir: root, NativeSessionID: "ses_synthetic"}
	got, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %q, %v", got, err)
	}
	ref.NativeSessionID = "ses_missing"
	got, err = p.ProbeNativeSession(context.Background(), ref)
	if err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("missing = %q, %v", got, err)
	}
}
