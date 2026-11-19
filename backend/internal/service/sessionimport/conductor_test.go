package sessionimport

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// newConductorDB creates root/conductor.db with the schema of a real
// Conductor 0.94.0 database.
func newConductorDB(t *testing.T, root string) (*sql.DB, func(string, ...any)) {
	t.Helper()
	schema, err := os.ReadFile("testdata/conductor-0.94.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "conductor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(schema))
	return db, exec
}

func TestConductorChatsPlaceWorkspacesLikeConductor(t *testing.T) {
	db, exec := newConductorDB(t, t.TempDir())
	exec(`INSERT INTO settings (key, value) VALUES ('conductor_root_path', ' /data/c ')`)
	exec(`INSERT INTO repos (id, name, root_path, storage_version) VALUES ('r1', 'one', '/src/one', 1), ('r2', 'two', '/src/two', 2), ('r3', 'three', '/src/three', 3), ('r4', 'four', '/src/four', NULL)`)
	exec(`INSERT INTO workspaces (id, local_id, repository_id, directory_name, workspace_path, state) VALUES
		('c1', 'w1', 'r1', 'berlin', NULL, 'ready'), ('c2', 'w2', 'r2', '.', NULL, 'ready'), ('c3', 'w3', 'r2', 'paris', '', 'archived'),
		('c4', 'w4', 'r3', 'tokyo', NULL, 'ready'), ('c5', 'w5', 'r3', 'lima', '/kept/lima', 'ready'), ('c6', 'w6', 'r4', 'oslo', NULL, 'ready')`)
	for i := 1; i <= 8; i++ {
		agent, hidden := "claude", 0
		switch i {
		case 7:
			agent = "cursor"
		case 8:
			hidden = 1
		}
		id := fmt.Sprintf("%d1111111-1111-4111-8111-111111111111", i)
		exec(`INSERT INTO sessions (id, workspace_id, agent_type, title, is_hidden, updated_at) VALUES (?, ?, ?, 't', ?, datetime('now', ?))`, id, fmt.Sprintf("w%d", min(i, 6)), agent, hidden, fmt.Sprintf("-%d minutes", i))
		exec(`INSERT INTO session_messages (id, session_id, role, content, sent_at) VALUES (?, ?, 'user', 'hi', datetime('now'))`, id, id)
	}
	chats, err := conductorChats(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, chat := range chats {
		got = append(got, chat.cwd+" @ "+chat.checkout)
	}
	want := []string{"/src/one/berlin @ /src/one/.main", "/src/two @ /src/two", "/src/two/.conductor/paris @ /src/two", "/data/c/workspaces/three/tokyo @ /src/three", "/kept/lima @ /src/three", "/src/four/oslo @ /src/four/.main"}
	if !slices.Equal(got, want) {
		t.Fatalf("folders = %q, want %q", got, want)
	}
}

func TestConductorSkipsAChatWithoutAWorkspaceFolder(t *testing.T) {
	root := t.TempDir()
	_, exec := newConductorDB(t, root)
	exec(`INSERT INTO repos (id, name, root_path, storage_version) VALUES ('r1', 'one', '/src/one', 2)`)
	exec(`INSERT INTO workspaces (id, local_id, repository_id) VALUES ('c1', 'w1', 'r1')`)
	exec(`INSERT INTO sessions (id, workspace_id, agent_type) VALUES ('11111111-1111-4111-8111-111111111111', 'w1', 'claude')`)
	exec(`INSERT INTO session_messages (id, session_id, role, content, sent_at) VALUES ('m1', '11111111-1111-4111-8111-111111111111', 'user', 'hi', datetime('now'))`)
	if _, err := readConductorTranscript(context.Background(), root, "11111111-1111-4111-8111-111111111111"); !errors.Is(err, errNoReadableConversation) {
		t.Fatalf("read = %v, want errNoReadableConversation", err)
	}
}

func TestConductorReadsAClosedWALDatabase(t *testing.T) {
	root := t.TempDir()
	db, exec := newConductorDB(t, root)
	exec(`PRAGMA journal_mode = WAL`)
	exec(`INSERT INTO workspaces (id, local_id, workspace_path) VALUES ('c1', 'w1', '/work')`)
	exec(`INSERT INTO sessions (id, workspace_id, agent_type) VALUES ('11111111-1111-4111-8111-111111111111', 'w1', 'claude')`)
	exec(`INSERT INTO session_messages (id, session_id, role, content, sent_at) VALUES ('m1', '11111111-1111-4111-8111-111111111111', 'user', 'hi', datetime('now'))`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "conductor.db-shm")); !os.IsNotExist(err) {
		t.Fatalf("a closed WAL database keeps no -shm file: %v", err)
	}
	files, err := discoverConductor(context.Background(), root)
	if err != nil || len(files) != 1 {
		t.Fatalf("discover = %#v, %v", files, err)
	}
	for _, suffix := range []string{"-shm", "-wal"} {
		if _, err := os.Stat(filepath.Join(root, "conductor.db"+suffix)); !os.IsNotExist(err) {
			t.Fatalf("reading wrote conductor.db%s next to Conductor's database: %v", suffix, err)
		}
	}
}

// TestConductorImportsEveryChat follows the formats of a real Conductor 0.94:
// session_messages rows from older chats, session_events from newer ones, and
// the Claude Code and Codex histories its agents keep.
func TestConductorImportsEveryChat(t *testing.T) {
	ctx := context.Background()
	project, conductor, claude, codex := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)
	live, gone := filepath.Join(project, ".conductor", "live"), filepath.Join(project, ".conductor", "gone")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	const (
		legacy, events, native = "0c3131e7-4d60-4684-a019-30bba52b5bac", "6a5b0b1e-3c55-4f0e-9a43-6d0b3b1e2f10", "22222222-2222-4222-8222-222222222222"
		lost                   = "77777777-7777-4777-8777-777777777777"
	)
	writeClaude(t, claude, native, live, "native prompt", time.Now())
	writeFile(t, filepath.Join(codex, "sessions", "2026", "11", "10", "rollout-2026-11-10T09-00-00-"+codexThread+".jsonl"), `{"timestamp":"2026-11-10T09:00:00Z","type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"`+live+`","originator":"codex_sdk_ts","source":"vscode"}}
{"timestamp":"2026-11-10T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Add a health check."}]}}
{"timestamp":"2026-11-10T09:00:09Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Added GET /health."}]}}
`)

	_, exec := newConductorDB(t, conductor)
	exec(`INSERT INTO repos (id, name, root_path, storage_version) VALUES ('r1', 'acme', ?, 2)`, project)
	exec(`INSERT INTO workspaces (id, local_id, repository_id, directory_name, state) VALUES ('c1', 'w1', 'r1', 'live', 'ready'), ('c2', 'w2', 'r1', 'gone', 'archived')`)
	exec(`INSERT INTO sessions (id, workspace_id, agent_type, claude_session_id, title, is_hidden, updated_at) VALUES
		(?, 'w1', 'claude', NULL, 'Code word', 0, datetime('now', '-90 days')),
		('s-events', 'w2', 'claude', NULL, 'Untitled', 0, datetime('now')),
		('s-native', 'w1', 'claude', ?, 'Native title', 0, datetime('now')),
		('s-codex', 'w1', 'codex', NULL, 'Health check', 0, datetime('now')),
		('s-lost', 'w1', 'codex', NULL, 'No rollout', 0, datetime('now')),
		('s-hidden', 'w1', 'claude', '55555555-5555-4555-8555-555555555555', 'Deleted', 1, datetime('now'))`, legacy, native)

	sdk := func(session, kind, payload string) string {
		return fmt.Sprintf(`{"type":%q,"session_id":%q,"parent_tool_use_id":null,%s}`, kind, session, payload)
	}
	rows := []struct{ role, content string }{
		{"user", "Read notes.txt and reply with its first line."},
		{"assistant", sdk(legacy, "system", `"subtype":"init","claude_code_version":"2.1.319","uuid":"9b0e7f3a-7a55-4c1b-9d61-0d4b0b5f3a11"`)},
		{"assistant", sdk(legacy, "assistant", `"uuid":"320feff9-215d-4d3c-abc7-c89b3916d782","message":{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"","signature":"CAQS"}]}`)},
		{"assistant", sdk(legacy, "assistant", `"uuid":"50f5bad4-e926-4f3e-9e1b-7c0a5f0e2b01","message":{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"notes.txt"}}]}`)},
		{"assistant", strings.Replace(sdk(legacy, "assistant", `"message":{"id":"msg_sub","role":"assistant","content":[{"type":"text","text":"subagent"}]}`), `"parent_tool_use_id":null`, `"parent_tool_use_id":"toolu_1"`, 1)},
		{"assistant", sdk(legacy, "user", `"uuid":"87ed4fbc-93eb-46ee-83e6-2ddeb16e9999","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"1\tpelican-42 is the code word"}]}`)},
		{"assistant", sdk(legacy, "assistant", `"uuid":"5dd9075a-6573-4bef-a0e0-b3cd4addf2b7","message":{"model":"claude-haiku-5-5","id":"msg_2","type":"message","role":"assistant","content":[{"type":"text","text":"pelican-42 is the code word"}]}`)},
		{"assistant", sdk(legacy, "result", `"subtype":"success","result":"pelican-42 is the code word"`)},
		{"user", sdk(legacy, "user", `"message":{"role":"user","content":[{"type":"text","text":"Read it again."}]}`)},
		{"assistant", sdk(legacy, "assistant", `"message":{"model":"claude-haiku-5-5","id":"msg_3","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"notes.txt"}}]}`)},
	}
	for i, row := range rows {
		exec(`INSERT INTO session_messages (id, session_id, role, content, sent_at) VALUES (?, ?, ?, ?, datetime('now', '-90 days', ?))`, fmt.Sprint("m", i), legacy, row.role, row.content, fmt.Sprintf("-%d seconds", len(rows)-i))
	}
	exec(`INSERT INTO session_messages (id, session_id, role, content, sent_at) VALUES ('queued', ?, 'user', 'queued, never sent', NULL)`, legacy)
	for _, id := range []string{"s-native", "s-hidden"} {
		exec(`INSERT INTO session_messages (id, session_id, role, content, sent_at) VALUES (?, ?, 'user', 'other chat', datetime('now'))`, "m-"+id, id)
	}
	event := func(session string, index int, kind, content string) {
		exec(`INSERT INTO session_events (session_id, session_index, event_id, event_type, content, received_at) VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?))`, session, index, fmt.Sprint("e", index), kind, content, fmt.Sprintf("-%d seconds", 100-index))
	}
	event("s-events", 0, "sessionHarness", `{"eventId":"e0","type":"sessionHarness","harness":"claude"}`)
	event("s-events", 1, "userMessage", `{"eventId":"e1","type":"userMessage","id":"u1","message":"Fix the login redirect.","state":"queued"}`)
	event("s-events", 2, "userMessage", `{"eventId":"e2","type":"userMessage","id":"u1","message":"Fix the login redirect.","state":"sent","turnId":"u1"}`)
	event("s-events", 3, "agent", `{"eventId":"e3","type":"agent","userMessageId":"u1","turnId":"u1","rawPayload":`+sdk(events, "assistant", `"uuid":"0e5b2a8e-1d7b-4f5c-9a3e-4c1f2b7d9e01","message":{"id":"msg_e","type":"message","role":"assistant","content":[{"type":"text","text":"Fixed: it now returns to the page you came from."}]}`)+`}`)
	event("s-events", 4, "agent", `not json`)
	event("s-codex", 0, "userMessage", `{"eventId":"e0","type":"userMessage","id":"u1","message":"Add a health check.","state":"sent"}`)
	event("s-codex", 1, "agent", `{"eventId":"e1","type":"agent","rawPayload":{"event":{"type":"thread.started","thread_id":"`+codexThread+`"},"thread_id":"`+codexThread+`"}}`)
	event("s-lost", 0, "userMessage", `{"eventId":"e0","type":"userMessage","id":"u1","message":"Hello","state":"sent"}`)
	event("s-lost", 1, "agent", `{"eventId":"e1","type":"agent","rawPayload":{"event":{"type":"thread.started","thread_id":"`+lost+`"},"thread_id":"`+lost+`"}}`)

	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{conductorSource: conductor, domain.HarnessClaudeCode: claude, domain.HarnessCodex: codex}
	preview, err := svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]Candidate{}
	for _, c := range preview.Candidates {
		if c.ProjectID != "repo" {
			t.Fatalf("candidate outside the project: %#v", c)
		}
		byTitle[string(c.Harness)+" "+c.Title] = c
	}
	want := map[string]int{"claude-code Code word": 3, "claude-code Fix the login redirect.": 2, "claude-code Native title": 1, "codex Health check": 2}
	if len(byTitle) != len(want) || len(preview.Candidates) != len(want) {
		t.Fatalf("candidates = %#v", preview.Candidates)
	}
	for key, count := range want {
		if c, ok := byTitle[key]; !ok || c.MessageCount != count {
			t.Fatalf("%s = %#v in %#v", key, c, preview.Candidates)
		}
	}

	ids := make([]string, 0, len(preview.Candidates))
	for _, c := range preview.Candidates {
		ids = append(ids, c.ID)
	}
	results, err := svc.Import(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status != "created" {
			t.Fatalf("import = %#v", results)
		}
	}
	paths := map[string]domain.SessionRecord{}
	for _, rec := range st.sessions {
		paths[rec.Metadata.AgentSessionID] = rec
	}
	for id, harness := range map[string]domain.AgentHarness{legacy: domain.HarnessClaudeCode, events: domain.HarnessClaudeCode, native: domain.HarnessClaudeCode, codexThread: domain.HarnessCodex} {
		rec := paths[id]
		root := canonical(claude)
		if harness == domain.HarnessCodex {
			root = canonical(codex)
		}
		if rec.Harness != harness || rec.Metadata.ImportSource == nil || rec.Metadata.ImportSource.ConfigDir != root {
			t.Fatalf("%s imported as %#v", id, rec)
		}
	}
	if got := paths[native].Metadata.NativeTranscriptPath; got != filepath.Join(canonical(claude), "projects", "project", native+".jsonl") {
		t.Fatalf("native chat was rewritten to %s", got)
	}
	if got := paths[events].Metadata.ImportSource.CWD; got != gone {
		t.Fatalf("archived chat cwd = %q, want %q", got, gone)
	}

	rebuilt, err := readTranscript(ctx, domain.HarnessClaudeCode, canonical(claude), paths[events].Metadata.NativeTranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, rebuilt.messages, "user: Fix the login redirect.", "assistant: Fixed: it now returns to the page you came from.")

	path := paths[legacy].Metadata.NativeTranscriptPath
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
		if (parent == nil) != (line.ParentUUID == nil) || parent != nil && *parent != *line.ParentUUID || line.SessionID != legacy || line.Version != "2.1.319" || line.CWD != live {
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

	again, err := svc.Scan(ctx)
	if err != nil || len(again.Candidates) != len(want) {
		t.Fatalf("rescan = %#v, %v", again, err)
	}
	for _, c := range again.Candidates {
		if _, ok := byTitle[string(c.Harness)+" "+c.Title]; !ok || c.SessionID == "" {
			t.Fatalf("imported chat offered again: %#v", c)
		}
	}
}
