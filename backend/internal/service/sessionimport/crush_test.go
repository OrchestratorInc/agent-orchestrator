package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCrushImportProjectDatabase(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	registry, err := json.Marshal(map[string]any{"projects": []map[string]string{{"path": "/work/source", "data_dir": data}}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "projects.json"), string(registry))
	db, err := sql.Open("sqlite", filepath.Join(data, "crush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT, updated_at INTEGER, message_count INTEGER)`,
		`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, parts TEXT, created_at INTEGER)`,
		`INSERT INTO sessions VALUES ('12345678-1234-1234-1234-123456789012', NULL, 'Synthetic chat', 1793613600, 3)`,
		`INSERT INTO messages VALUES ('u', '12345678-1234-1234-1234-123456789012', 'user', '[{"type":"text","data":{"text":"Synthetic prompt"}},{"type":"text","data":{"text":"injected","hidden":true}}]', 1793613600)`,
		`INSERT INTO messages VALUES ('a', '12345678-1234-1234-1234-123456789012', 'assistant', '[{"type":"text","data":{"text":"Synthetic answer"}},{"type":"reasoning","data":{"thinking":"private"}}]', 1793613601)`,
		`INSERT INTO messages VALUES ('t', '12345678-1234-1234-1234-123456789012', 'tool', '[{"type":"text","data":{"text":"tool output"}}]', 1793613602)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	files, err := discoverCrush(context.Background(), root)
	if err != nil || len(files) != 1 {
		t.Fatalf("discover = %v, %v", files, err)
	}
	got, err := readTranscript(context.Background(), domain.HarnessCrush, root, files[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Synthetic prompt", "assistant: Synthetic answer")
	if got.cwd != "/work/source" || got.nativeID != files[0].nativeID() || got.title != "Synthetic chat" {
		t.Fatalf("metadata = %+v", got)
	}
}
