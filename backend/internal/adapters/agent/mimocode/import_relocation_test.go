package mimocode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMiMoCodeImportedDatabaseRelocation(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "mimocode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE project (id TEXT PRIMARY KEY)`,
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, project_id TEXT, directory TEXT, workspace_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT)`,
		`CREATE TABLE __drizzle_migrations (id INTEGER PRIMARY KEY, hash TEXT)`,
		`INSERT INTO project VALUES ('project')`,
		`INSERT INTO __drizzle_migrations VALUES (1, 'synthetic-schema')`,
		`INSERT INTO session VALUES ('ses_synthetic', NULL, 'project', '/source', 'old-workspace'), ('ses_other', NULL, 'project', '/source', NULL)`,
		`INSERT INTO message VALUES ('selected', 'ses_synthetic', 'synthetic prompt'), ('other', 'ses_other', 'other prompt')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	workspace := t.TempDir()
	r := ports.ImportedSessionRelocation{NativeSessionID: "ses_synthetic", ConfigDir: root, LaunchCWD: workspace}
	p := New()
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, ".mimocode", "mimocode.db")
	copyDB, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	var directory string
	var workspaceID sql.NullString
	if err := copyDB.QueryRow(`SELECT directory, workspace_id FROM session WHERE id = 'ses_synthetic'`).Scan(&directory, &workspaceID); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil || directory != resolved || workspaceID.Valid {
		t.Fatalf("copy identity = %q, %v, %v", directory, workspaceID, err)
	}
	var count int
	if err := copyDB.QueryRow(`SELECT COUNT(*) FROM session`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("copy sessions = %d, %v", count, err)
	}
	if _, err := copyDB.Exec(`INSERT INTO message VALUES ('ao-turn', 'ses_synthetic', 'later AO turn')`); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := copyDB.QueryRow(`SELECT COUNT(*) FROM message`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("later turns = %d, %v", count, err)
	}
	if err := db.QueryRow(`SELECT directory FROM session WHERE id = 'ses_synthetic'`).Scan(&directory); err != nil || directory != "/source" {
		t.Fatalf("source changed = %q, %v", directory, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy permissions = %v, %v", info, err)
	}
	t.Run("symlinked launch directory", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "workspace-link")
		if err := os.Symlink(workspace, link); err != nil {
			t.Skip(err)
		}
		r.LaunchCWD = link
		if err := p.RelocateImportedSession(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	})
}
