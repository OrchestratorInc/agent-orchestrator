package sessionmanager

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/crush"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestCrushFirstOpenExcludesUnignoredNativeDatabaseFromTransfer(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(map[bool]string{false: "untracked", true: "staged"}[staged], func(t *testing.T) {
			ctx := context.Background()
			m, store, _, source := newGitTaskPreparationManager(t)
			plugin := crush.New()
			m.agents = singleAgent{agent: plugin}
			dir := filepath.Join(source, ".crush")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "crush.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`CREATE TABLE sessions (id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT, updated_at INTEGER, message_count INTEGER)`,
				`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, parts TEXT, created_at INTEGER)`,
				`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY, version_id INTEGER, is_applied INTEGER)`,
				`INSERT INTO goose_db_version VALUES (1, 20261102000000, 1)`,
				`INSERT INTO sessions VALUES ('12345678-1234-1234-1234-123456789012', NULL, 'selected', 100, 1), ('87654321-1234-1234-1234-123456789012', NULL, 'other', 100, 1)`,
				`INSERT INTO messages VALUES ('selected-message', '12345678-1234-1234-1234-123456789012', 'user', '[]', 100), ('other-message', '87654321-1234-1234-1234-123456789012', 'user', '[]', 100)`,
			} {
				if _, err := db.Exec(statement); err != nil {
					_ = db.Close()
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Even inert leftover sidecars must not travel as project files.
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if err := os.WriteFile(path+suffix, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ordinary project file"), 0o600); err != nil {
				t.Fatal(err)
			}
			if staged {
				if _, err := importGit(ctx, source, "add", ".crush"); err != nil {
					t.Fatal(err)
				}
			}
			id := "12345678-1234-1234-1234-123456789012"
			rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCrush, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle}}
			rec.Metadata = domain.SessionMetadata{AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: filepath.Join(path, id), ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: dir, CWD: source}}
			store.sessions[rec.ID] = rec
			prepared, err := m.prepareImportedWorkspace(ctx, rec, store.projects["mer"])
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(prepared.Metadata.WorkspacePath, ".crush", "crush.db")
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				if _, err := os.Lstat(target + suffix); !os.IsNotExist(err) {
					t.Fatalf("raw database transferred (%q): %v", suffix, err)
				}
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(target), "notes.txt")); err != nil {
				t.Fatal(err)
			}
			adopted, err := m.relocateImportedSession(ctx, prepared, prepared.Metadata.WorkspacePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			if adopted.Metadata.ImportSource.ResumeNativeID != id {
				t.Fatal("selected conversation was not checkpointed")
			}
			copyDB, err := sql.Open("sqlite", target)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = copyDB.Close() }()
			var sessions, messages int
			if err := copyDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if err := copyDB.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, id).Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if sessions != 1 || messages != 1 {
				t.Fatalf("selected copy has %d sessions/%d selected messages", sessions, messages)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("source database changed: %v", err)
			}
		})
	}
}
