package sessionsearch_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
)

// claudeToolUseLine is an assistant record that calls a tool.
func claudeToolUseLine(t *testing.T, name, arguments, timestamp string) string {
	t.Helper()
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"` +
		name + `","input":` + arguments + `}]},"timestamp":"` + timestamp + `"}`
}

// claudeToolResultLine is a user record that carries a tool's output.
func claudeToolResultLine(t *testing.T, output, timestamp string) string {
	t.Helper()
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` +
		output + `"}]},"timestamp":"` + timestamp + `"}`
}

func seedTUI(t *testing.T, store interface {
	CreateSession(context.Context, domain.SessionRecord) (domain.SessionRecord, error)
}, projectID, sessionID string, harness domain.AgentHarness) domain.SessionRecord {
	t.Helper()
	session, err := store.CreateSession(context.Background(), domain.SessionRecord{
		ID:        domain.SessionID(sessionID),
		ProjectID: domain.ProjectID(projectID),
		Kind:      domain.KindWorker,
		Harness:   harness,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/" + projectID},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return session
}

// Claude Code writes tool calls and tool results into the transcript. Both are
// part of "every word of the session" and neither is prose.
func TestNativeClaudeToolCallsAndOutputAreIndexed(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-native", domain.HarnessClaudeCode)
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "why is the build failing", "2026-08-09T12:00:00Z"),
		claudeToolUseLine(t, "Bash", `{"command":"go build ./..."}`, "2026-08-09T12:00:01Z"),
		claudeToolResultLine(t, "undefined symbol zzNeedle in package cache", "2026-08-09T12:00:02Z"),
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	tool := search(t, f.svc, "go build")
	if len(tool.Results) != 1 || tool.Results[0].Kind != domain.SessionSearchKindTool {
		t.Fatalf("tool call results = %+v, want the tool invocation", tool.Results)
	}
	output := search(t, f.svc, "zzNeedle")
	if len(output.Results) != 1 || output.Results[0].Kind != domain.SessionSearchKindCommand {
		t.Fatalf("tool output results = %+v, want the tool result", output.Results)
	}
}

// Codex records a shell command as an item_completed CommandExecution with its
// output; that output is searchable like any other text.
func TestNativeCodexCommandOutputIsIndexed(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-codex", domain.HarnessCodex)
	writeTranscript(t, root, filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+string(session.ID)+".jsonl"),
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"`+string(session.ID)+`"}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","npm run typecheck"],"aggregated_output":"error TS2304: cannot find name zzNeedle","exit_code":2,"status":"completed"}}}`,
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	results := search(t, f.svc, "TS2304")
	if len(results.Results) != 1 || results.Results[0].Kind != domain.SessionSearchKindCommand {
		t.Fatalf("codex command output = %+v", results.Results)
	}
}

// OpenCode keeps its history in its own SQLite database. The indexer must read
// it read-only and pick up both the newer session_message schema and the older
// message one.
func TestOpenCodeStoreIsIndexedReadOnly(t *testing.T) {
	dataHome := t.TempDir()
	databaseDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(databaseDir, 0o750); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	databasePath := filepath.Join(databaseDir, "opencode.db")
	seedOpenCodeDatabase(t, databasePath)

	before, err := os.Stat(databasePath)
	if err != nil {
		t.Fatalf("stat opencode db: %v", err)
	}

	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: dataHome})
	seedTUIProject(t, f.store, "proj")
	seedTUI(t, f.store, "proj", "sess-opencode", domain.HarnessOpenCode)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	results := search(t, f.svc, "deadlock")
	if len(results.Results) != 1 {
		t.Fatalf("opencode results = %+v, want the user message", results.Results)
	}
	if results.Results[0].Kind != domain.SessionSearchKindUser {
		t.Fatalf("kind = %q, want user", results.Results[0].Kind)
	}
	if got := search(t, f.svc, "row-level lock"); len(got.Results) != 1 {
		t.Fatalf("opencode assistant reply missing: %+v", got.Results)
	}

	after, err := os.Stat(databasePath)
	if err != nil {
		t.Fatalf("stat opencode db after index: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("opencode store changed: size %d->%d mtime %v->%v",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}

func seedOpenCodeDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer func() { _ = db.Close() }()
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT NOT NULL, directory TEXT,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed opencode schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session (id, title, directory, time_created, time_updated)
		VALUES ('sess-opencode', 'Deadlock investigation', '/tmp/proj', 1, 2)`); err != nil {
		t.Fatalf("seed opencode session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES ('m1', 'sess-opencode', 'user', 1, 1, 1, '{"text":"the deadlock happens on shutdown"}')`); err != nil {
		t.Fatalf("seed opencode user message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES ('m2', 'sess-opencode', 'assistant', 2, 2, 2, '{"text":"it waits for the row-level lock that the writer holds"}')`); err != nil {
		t.Fatalf("seed opencode assistant message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES ('m3', 'sess-opencode', 'idle', 3, 3, 3, '{"outcome":"ok"}')`); err != nil {
		t.Fatalf("seed opencode idle message: %v", err)
	}
}

// The older OpenCode schema stores one row per message with no type column.
func TestOpenCodeLegacySchemaIsIndexed(t *testing.T) {
	dataHome := t.TempDir()
	databaseDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(databaseDir, 0o750); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	databasePath := filepath.Join(databaseDir, "opencode.db")
	db, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT NOT NULL, directory TEXT,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed legacy schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session VALUES ('sess-opencode', 'Legacy store', '/tmp/proj', 1, 2)`); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO message VALUES ('m1', 'sess-opencode', 1, 1,
		'{"role":"user","content":[{"type":"text","text":"legacy schema probe"}]}')`); err != nil {
		t.Fatalf("seed legacy message: %v", err)
	}
	_ = db.Close()

	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: dataHome})
	seedTUIProject(t, f.store, "proj")
	seedTUI(t, f.store, "proj", "sess-opencode", domain.HarnessOpenCode)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "legacy schema probe"); len(got.Results) != 1 {
		t.Fatalf("legacy opencode message missing: %+v", got.Results)
	}
}

// A missing OpenCode install must not be reported as an error the person sees;
// the session stays findable by title.
func TestOpenCodeWithoutAStoreStaysFindableByTitle(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: t.TempDir()})
	seedTUIProject(t, f.store, "proj")
	record := seedTUI(t, f.store, "proj", "sess-opencode", domain.HarnessOpenCode)
	record.DisplayName = "Deadlock investigation"
	if err := f.store.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	// UpdateSession no longer writes the title; renames have their own narrow writer.
	if _, err := f.store.RenameSession(context.Background(), record.ID, record.DisplayName, time.Now()); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "Deadlock"); len(got.Results) != 1 {
		t.Fatalf("title of an OpenCode session missing: %+v", got.Results)
	}
}

// The index must never hold a lock on a provider database after a pass.
func TestOpenCodeStoreIsReleasedAfterIndexing(t *testing.T) {
	dataHome := t.TempDir()
	databaseDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(databaseDir, 0o750); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	databasePath := filepath.Join(databaseDir, "opencode.db")
	seedOpenCodeDatabase(t, databasePath)
	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: dataHome})
	seedTUIProject(t, f.store, "proj")
	seedTUI(t, f.store, "proj", "sess-opencode", domain.HarnessOpenCode)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	// A read-only open of the same file succeeds only if no writer is holding it
	// in a way that blocks readers; more importantly, the file is removable,
	// which proves the reader closed its handles.
	if err := os.Remove(databasePath); err != nil {
		t.Fatalf("opencode store still held open after indexing: %v", err)
	}
	_ = time.Now
}

// Every Codex rollout that writes the item stream also writes the
// response_item stream for the same turns. Reading both doubled every match.
func TestNativeCodexRolloutWithBothStreamsIndexesEachMessageOnce(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-codex", domain.HarnessCodex)
	writeTranscript(t, root, filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+string(session.ID)+".jsonl"),
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"`+string(session.ID)+`"}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the reaper leak goroutines"}]}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"why does the reaper leak goroutines"}]}}}`,
		`{"timestamp":"2026-08-09T12:00:02Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Update File: src/reaper.go\n+ticker.Stop()"}}`,
		`{"timestamp":"2026-08-09T12:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","changes":{"src/reaper.go":{"type":"update","unified_diff":"@@ -1 +1 @@\n-\tgo tick()\n+\tdefer quokkaTicker.Stop()\n","move_path":null}}}}}`,
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	results := search(t, f.svc, "goroutines")
	if len(results.Results) != 1 || results.Results[0].SessionMatches != 1 {
		t.Fatalf("results = %+v, want one session with one match", results.Results)
	}
	// The edit is found by a symbol it added and by the file it touched.
	if got := search(t, f.svc, "quokkaTicker"); len(got.Results) != 1 || got.Results[0].Kind != domain.SessionSearchKindPath {
		t.Fatalf("file change = %+v", got.Results)
	}
	if got := search(t, f.svc, "reaper path:src/reaper.go"); len(got.Results) != 1 {
		t.Fatalf("path filter = %+v", got.Results)
	}
}

// A transcript indexed by an older reader is read again from the start, so
// text the old reader skipped (here a prompt queued while Claude was busy)
// becomes findable without the transcript changing.
func TestNativeTranscriptIsReadAgainAfterAReaderUpgrade(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-native", domain.HarnessClaudeCode)
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "the checkout button is misaligned", "2026-08-09T12:00:00Z"),
		`{"type":"attachment","timestamp":"2026-08-09T12:00:03Z","attachment":{"type":"queued_command","prompt":"also fix the quokka footer","commandMode":"prompt"}}`,
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	sourceKey := sessionsearch.NativeSourceKey(domain.HarnessClaudeCode, string(session.ID))
	source, ok, err := f.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil || !ok {
		t.Fatalf("source = %v %v", ok, err)
	}

	// Rewind to what the previous reader left: the whole file consumed, the
	// queued prompt never indexed, and no reader format recorded.
	if err := f.store.DeleteSessionSearchDocsForSource(ctx, sourceKey); err != nil {
		t.Fatalf("delete docs: %v", err)
	}
	source.Fingerprint = ""
	if err := f.store.UpsertSessionSearchSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if got := search(t, f.svc, "quokka"); len(got.Results) != 0 {
		t.Fatalf("setup left the prompt indexed: %+v", got.Results)
	}

	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll after upgrade: %v", err)
	}
	if got := search(t, f.svc, "quokka"); len(got.Results) != 1 || got.Results[0].Kind != domain.SessionSearchKindUser {
		t.Fatalf("queued prompt after upgrade = %+v", got.Results)
	}
	if got := search(t, f.svc, "misaligned"); len(got.Results) != 1 || got.Results[0].SessionMatches != 1 {
		t.Fatalf("re-read duplicated the earlier line: %+v", got.Results)
	}
}

// A rollout read before Codex wrote its first item_completed line is read from
// the response_item stream. When the item stream appears, the source is read
// again from the item stream rather than indexing the first turn from both.
func TestNativeCodexRolloutThatGainsItsItemStreamIsNotDoubled(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-codex", domain.HarnessCodex)
	path := writeTranscript(t, root, filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+string(session.ID)+".jsonl"),
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"`+string(session.ID)+`"}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the reaper leak goroutines"}]}}`,
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "goroutines"); len(got.Results) != 1 {
		t.Fatalf("first pass = %+v", got.Results)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	for _, line := range []string{
		`{"timestamp":"2026-08-09T12:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"why does the reaper leak goroutines"}]}}}`,
		`{"timestamp":"2026-08-09T12:00:09Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the quokka ticker is never stopped"}]}}`,
		`{"timestamp":"2026-08-09T12:00:09Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"the quokka ticker is never stopped"}]}}}`,
	} {
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatalf("append rollout: %v", err)
		}
	}
	_ = file.Close()

	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for _, query := range []string{"goroutines", "quokka"} {
		if got := search(t, f.svc, query); len(got.Results) != 1 || got.Results[0].SessionMatches != 1 {
			t.Fatalf("%s after the item stream appeared = %+v, want one match", query, got.Results)
		}
	}
}
