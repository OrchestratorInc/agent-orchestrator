package sessionimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// All session rows and text are synthetic, never copied from provider history.
func TestGooseScanAndTerminalImport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "sessions", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, query := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT, description TEXT, working_dir TEXT,
		updated_at TEXT, session_type TEXT, archived_at TEXT, parent_session_id TEXT)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content_json TEXT,
		created_timestamp INTEGER, metadata_json TEXT)`,
		`INSERT INTO sessions VALUES ('20261102_1', 'New Chat', '', '', '2026-11-02T08:00:00Z', 'user', NULL, NULL)`,
		`INSERT INTO sessions VALUES ('child', 'Child', '', '', '', 'user', NULL, '20261102_1')`,
		`INSERT INTO sessions VALUES ('archived', 'Archived', '', '', '', 'user', '2026-11-01', NULL)`,
		`INSERT INTO messages VALUES (1, '20261102_1', 'user', '[{"type":"text","text":"remember the synthetic otter"}]', 1793606400, '{"userVisible":true}')`,
		`INSERT INTO messages VALUES (2, '20261102_1', 'user', '[{"type":"text","text":"injected context"}]', 1793606401, '{"userVisible":false}')`,
		`INSERT INTO messages VALUES (3, '20261102_1', 'assistant', '[{"type":"toolRequest","name":"read"},{"type":"text","text":"remembered"}]', 1793606402, '{}')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	st := &importStore{}
	svc := New(st, "", func(domain.AgentHarness) bool { return false })
	svc.roots = map[domain.AgentHarness]string{domain.HarnessGoose: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	c := preview.Candidates[0]
	if c.MessageCount != 2 || c.Title != "remember the synthetic otter" {
		t.Fatalf("candidate = %#v", c)
	}
	results, err := svc.Import(context.Background(), []string{c.ID})
	if err != nil || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	if rec := st.sessions[0]; rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != "20261102_1" || len(st.messages) != 2 {
		t.Fatalf("record = %#v, messages = %#v", rec, st.messages)
	}
	readOnly, err := gooseDB(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readOnly.Close() }()
	if _, err := readOnly.Exec("DELETE FROM sessions"); err == nil {
		t.Fatal("provider database was writable")
	}
}

func TestGooseRootOverrides(t *testing.T) {
	pathRoot, xdg := t.TempDir(), t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", pathRoot)
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := gooseRoot(t.TempDir()); got != filepath.Join(pathRoot, "data") {
		t.Fatal(got)
	}
	t.Setenv("GOOSE_PATH_ROOT", "")
	if got := gooseRoot(t.TempDir()); got != filepath.Join(xdg, "goose") {
		t.Fatal(got)
	}
}
