package sessionimport

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestConductorChatsPlaceWorkspacesWithoutAPath(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "conductor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, query := range []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE repos (id TEXT PRIMARY KEY, name TEXT, root_path TEXT, storage_version INTEGER)`,
		`CREATE TABLE workspaces (id TEXT PRIMARY KEY, repository_id TEXT, directory_name TEXT, workspace_path TEXT)`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, updated_at TEXT)`,
		`CREATE TABLE session_messages (session_id TEXT, role TEXT, content TEXT, created_at TEXT, sent_at TEXT)`,
		`INSERT INTO settings VALUES ('conductor_root_path', ' /data/c ')`,
		`INSERT INTO repos VALUES ('r1', 'one', '/src/one', 1), ('r2', 'two', '/src/two', 2), ('r3', 'three', '/src/three', 3)`,
		`INSERT INTO workspaces VALUES ('w1', 'r1', 'berlin', NULL), ('w2', 'r2', '.', NULL), ('w3', 'r2', 'paris', ''), ('w4', 'r3', 'tokyo', NULL), ('w5', 'r3', 'lima', '/kept/lima')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("%d1111111-1111-4111-8111-111111111111", i)
		if _, err := db.Exec(`INSERT INTO sessions VALUES (?, ?, 't', datetime('now', ?))`, id, fmt.Sprintf("w%d", i), fmt.Sprintf("-%d minutes", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO session_messages VALUES (?, 'user', 'hi', datetime('now'), datetime('now'))`, id); err != nil {
			t.Fatal(err)
		}
	}
	chats, err := conductorChats(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, chat := range chats {
		got = append(got, chat.cwd)
	}
	if want := []string{"/src/one/berlin", "/src/two", "/src/two/.conductor/paris", "/data/c/workspaces/three/tokyo", "/kept/lima"}; !slices.Equal(got, want) {
		t.Fatalf("folders = %q, want %q", got, want)
	}
}

func TestConductorChatImportsAsClaudeTranscript(t *testing.T) {
	ctx := context.Background()
	project, conductor, claude := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	const chat, known = "0c3131e7-4d60-4684-a019-30bba52b5bac", "22222222-2222-4222-8222-222222222222"
	writeClaude(t, claude, known, project, "already in Claude Code", time.Now())

	db, err := sql.Open("sqlite", filepath.Join(conductor, "conductor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE workspaces (id TEXT PRIMARY KEY, local_id TEXT, workspace_path TEXT, state TEXT, DEPRECATED_archived INTEGER DEFAULT 0, remote_archived_at TEXT)`)
	exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, agent_type TEXT, claude_session_id TEXT, title TEXT DEFAULT 'Untitled', is_hidden INTEGER DEFAULT 0, updated_at TEXT DEFAULT (datetime('now')))`)
	exec(`CREATE TABLE session_messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, created_at TEXT DEFAULT (datetime('now')), sent_at TEXT)`)
	exec(`INSERT INTO workspaces VALUES ('w1', 'local-1', ?, 'ready', 0, NULL), ('w2', 'local-2', ?, 'archived', 0, NULL)`, project, project)
	exec(`INSERT INTO sessions (id, workspace_id, agent_type, claude_session_id, title, is_hidden) VALUES
		('s1', 'local-1', 'claude', ?, 'Code word', 0),
		('s2', 'local-2', 'claude', '33333333-3333-4333-8333-333333333333', 'Archived', 0),
		('s3', 'local-1', 'codex', '44444444-4444-4444-8444-444444444444', 'Codex', 0),
		('s4', 'local-1', 'claude', '55555555-5555-4555-8555-555555555555', 'Hidden', 1),
		('s5', 'w1', NULL, ?, 'Known', 0)`, chat, known)
	sdk := func(kind, payload string) string {
		return fmt.Sprintf(`{"type":%q,"session_id":%q,"parent_tool_use_id":null,%s}`, kind, chat, payload)
	}
	rows := []struct{ role, content string }{
		{"user", "Read notes.txt and reply with its first line."},
		{"assistant", sdk("system", `"subtype":"init","claude_code_version":"2.1.319","uuid":"9b0e7f3a-7a55-4c1b-9d61-0d4b0b5f3a11"`)},
		{"assistant", sdk("assistant", `"uuid":"320feff9-215d-4d3c-abc7-c89b3916d782","message":{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"","signature":"CAQS"}]}`)},
		{"assistant", sdk("assistant", `"uuid":"50f5bad4-e926-4f3e-9e1b-7c0a5f0e2b01","message":{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"notes.txt"}}]}`)},
		{"assistant", strings.Replace(sdk("assistant", `"message":{"id":"msg_sub","role":"assistant","content":[{"type":"text","text":"subagent"}]}`), `"parent_tool_use_id":null`, `"parent_tool_use_id":"toolu_1"`, 1)},
		{"assistant", sdk("user", `"uuid":"87ed4fbc-93eb-46ee-83e6-2ddeb16e9999","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"1\tpelican-42 is the code word"}]}`)},
		{"assistant", sdk("assistant", `"uuid":"5dd9075a-6573-4bef-a0e0-b3cd4addf2b7","message":{"model":"claude-haiku-5-5","id":"msg_2","type":"message","role":"assistant","content":[{"type":"text","text":"pelican-42 is the code word"}]}`)},
		{"assistant", sdk("result", `"subtype":"success","result":"pelican-42 is the code word"`)},
		{"user", sdk("user", `"message":{"role":"user","content":[{"type":"text","text":"Read it again."}]}`)},
		{"assistant", sdk("assistant", `"message":{"model":"claude-haiku-5-5","id":"msg_3","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"notes.txt"}}]}`)},
	}
	for i, row := range rows {
		exec(`INSERT INTO session_messages (session_id, role, content, sent_at) VALUES ('s1', ?, ?, datetime('now', ?))`, row.role, row.content, fmt.Sprintf("-%d seconds", len(rows)-i))
	}
	exec(`INSERT INTO session_messages (session_id, role, content, sent_at) VALUES ('s1', 'user', 'queued, never sent', NULL)`)
	for _, id := range []string{"s2", "s3", "s4", "s5"} {
		exec(`INSERT INTO session_messages (session_id, role, content, sent_at) VALUES (?, 'user', 'other chat', datetime('now'))`, id)
	}

	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{conductorSource: conductor}
	preview, err := svc.Scan(ctx)
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	if c := preview.Candidates[0]; c.Harness != domain.HarnessClaudeCode || c.Title != "Code word" || c.ProjectID != "repo" || c.MessageCount != 3 {
		t.Fatalf("candidate = %#v", c)
	}
	results, err := svc.Import(ctx, []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	rec := st.sessions[0]
	path := rec.Metadata.NativeTranscriptPath
	if rec.Harness != domain.HarnessClaudeCode || rec.Metadata.AgentSessionID != chat || rec.Metadata.ImportSource.ConfigDir != canonical(claude) || filepath.Base(path) != chat+".jsonl" {
		t.Fatalf("record = %#v", rec)
	}

	native, err := readTranscript(ctx, domain.HarnessClaudeCode, canonical(claude), path)
	if err != nil {
		t.Fatal(err)
	}
	text := func(messages []Message) []string {
		var out []string
		for _, m := range messages {
			out = append(out, string(m.Role)+": "+m.Text)
		}
		return out
	}
	want := []string{"user: Read notes.txt and reply with its first line.", "assistant: pelican-42 is the code word", "user: Read it again."}
	if got := text(st.messages); !slices.Equal(got, want) || !slices.Equal(text(native.messages), want) || native.cwd != project {
		t.Fatalf("archive = %q, Claude reads %q in %q", got, text(native.messages), native.cwd)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var parent *string
	var tools []string
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		var line claudeLine
		if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if (parent == nil) != (line.ParentUUID == nil) || parent != nil && *parent != *line.ParentUUID || line.SessionID != chat || line.Version != "2.1.319" || line.CWD != project {
			t.Fatalf("line breaks the chain: %s", lines.Bytes())
		}
		parent = &line.UUID
		for _, id := range []string{"toolu_1", "toolu_2", "subagent"} {
			if strings.Contains(string(line.Message), id) {
				tools = append(tools, line.Type+":"+id)
			}
		}
	}
	if !slices.Equal(tools, []string{"assistant:toolu_1", "user:toolu_1"}) {
		t.Fatalf("tool records = %q", tools)
	}

	svc.roots[domain.HarnessClaudeCode] = claude
	again, err := svc.Scan(ctx)
	if err != nil || len(again.Candidates) != 2 {
		t.Fatalf("rescan = %#v, %v", again, err)
	}
	for _, c := range again.Candidates {
		if c.Title == "Code word" && c.SessionID != rec.ID {
			t.Fatalf("imported chat offered again: %#v", again.Candidates)
		}
	}
}
