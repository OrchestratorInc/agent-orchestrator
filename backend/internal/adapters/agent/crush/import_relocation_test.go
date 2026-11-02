package crush

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCrushImportRelocationKeepsSourceAndLaterTurns(t *testing.T) {
	source := filepath.Join(t.TempDir(), "crush.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	id := "12345678-1234-1234-1234-123456789012"
	for _, stmt := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT, updated_at INTEGER, message_count INTEGER)`,
		`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, parts TEXT, created_at INTEGER)`,
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY, version_id INTEGER, is_applied INTEGER)`,
		`INSERT INTO goose_db_version VALUES (1, 20261102000000, 1)`,
		`INSERT INTO sessions VALUES ('12345678-1234-1234-1234-123456789012', NULL, 'selected', 100, 1), ('87654321-1234-1234-1234-123456789012', NULL, 'other', 100, 1)`,
		`INSERT INTO messages VALUES ('selected-message', '12345678-1234-1234-1234-123456789012', 'user', '[]', 100), ('other-message', '87654321-1234-1234-1234-123456789012', 'user', '[]', 100)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	workspace := t.TempDir()
	r := ports.ImportedSessionRelocation{NativeSessionID: id, TranscriptPath: filepath.Join(source, id), LaunchCWD: workspace}
	p := New()
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, ".crush", "crush.db")
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private copy = %v, %v", info, err)
	}
	copyDB, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	var count int
	if err := copyDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("copied sessions = %d, %v", count, err)
	}
	if _, err := copyDB.Exec(`INSERT INTO messages VALUES ('ao-turn', '12345678-1234-1234-1234-123456789012', 'assistant', '[]', 101)`); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := copyDB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("later turns = %d, %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("source = %d, %v", count, err)
	}
}
