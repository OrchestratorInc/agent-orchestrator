package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDevinImportsNativeHistoryAsTerminal(t *testing.T) {
	root, cwd := t.TempDir(), canonical(t.TempDir())
	if err := os.Mkdir(filepath.Join(root, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "cli", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE refinery_schema_history(version INTEGER PRIMARY KEY);
CREATE TABLE sessions(id TEXT PRIMARY KEY,working_directory TEXT,backend_type TEXT,model TEXT,title TEXT,main_chain_id INTEGER,last_activity_at INTEGER,hidden INTEGER,workspace_dirs TEXT,cogs_json TEXT,metadata TEXT);
CREATE TABLE message_nodes(session_id TEXT,node_id INTEGER,parent_node_id INTEGER,chat_message TEXT,created_at INTEGER);
CREATE TABLE subagent_heads(session_id TEXT);`); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 20; n++ {
		if _, err := db.Exec("INSERT INTO refinery_schema_history VALUES(?)", n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES('native-sage',?,'windsurf','native-model','native source',0,?,0,'[]','[]','{}')`, cwd, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	message, _ := json.Marshal(map[string]any{"message_id": "user", "role": "user", "content": "remember native marker", "metadata": map[string]bool{"is_user_input": true}})
	if _, err := db.Exec("INSERT INTO message_nodes VALUES('native-sage',0,NULL,?,?)", string(message), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: cwd}}}
	svc := New(st, t.TempDir(), chatSupported)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessDevin: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview=%#v,%v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import=%#v,%v", results, err)
	}
	r := st.sessions[0]
	if r.Mode != domain.SessionModeTUI || r.Metadata.AgentSessionID != "native-sage" || r.Metadata.ImportSource.Fingerprint == "" || !r.NeedsImportResume() || len(st.messages) != 1 {
		t.Fatalf("imported record=%#v", r)
	}
}
