package crush

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCrushImportedNativeProbe(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	id := "12345678-1234-1234-1234-123456789012"
	dbPath := filepath.Join(data, "crush.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY); INSERT INTO sessions VALUES ('12345678-1234-1234-1234-123456789012')`); err != nil {
		t.Fatal(err)
	}
	registry, err := json.Marshal(map[string]any{"projects": []map[string]string{{"path": data, "data_dir": data}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects.json"), registry, 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	config, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"CRUSH_GLOBAL_DATA": root})
	if err != nil || config != root {
		t.Fatalf("config = %q, %v", config, err)
	}
	ref := ports.NativeSessionRef{ConfigDir: root, NativeSessionID: id}
	path, ok, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !ok || path != filepath.Join(dbPath, id) {
		t.Fatalf("locate = %q, %v, %v", path, ok, err)
	}
	availability, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || availability != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("available = %q, %v", availability, err)
	}
	if _, err := db.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	availability, err = p.ProbeNativeSession(context.Background(), ref)
	if err != nil || availability != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("missing registry backing = %q, %v", availability, err)
	}
}
