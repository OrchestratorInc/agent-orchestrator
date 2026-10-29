package sessionimport

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// newOpenCodeStore builds a synthetic opencode.db that mirrors the columns the
// importer reads. Content is wholly invented; no real user history is used.
func newOpenCodeStore(t *testing.T) (root string, db *sql.DB) {
	t.Helper()
	root = t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "opencode.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE session (
			id text PRIMARY KEY, parent_id text, directory text NOT NULL,
			title text NOT NULL, time_updated integer NOT NULL, time_archived integer)`,
		`CREATE TABLE message (
			id text PRIMARY KEY, session_id text NOT NULL,
			time_created integer NOT NULL, data text NOT NULL)`,
		`CREATE TABLE part (
			id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL,
			time_created integer NOT NULL, data text NOT NULL)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return root, db
}

func addSession(t *testing.T, db *sql.DB, id, parent, dir, title string, updated int64, archived *int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO session (id, parent_id, directory, title, time_updated, time_archived) VALUES (?, ?, ?, ?, ?, ?)`,
		id, parent, dir, title, updated, archived); err != nil {
		t.Fatalf("add session: %v", err)
	}
}

// addMessage writes a message and its ordered parts. A part is "role:text",
// "tool", "reasoning", or "step-start" to exercise the text-only filter.
func addMessage(t *testing.T, db *sql.DB, sessionID, msgID, role string, created int64, parts ...string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
		msgID, sessionID, created, fmt.Sprintf(`{"role":%q}`, role)); err != nil {
		t.Fatalf("add message: %v", err)
	}
	for i, part := range parts {
		partID := fmt.Sprintf("%s_p%d", msgID, i)
		var data string
		if len(part) > 5 && part[:5] == "text:" {
			data = fmt.Sprintf(`{"type":"text","text":%q}`, part[5:])
		} else {
			data = fmt.Sprintf(`{"type":%q}`, part)
		}
		if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, data) VALUES (?, ?, ?, ?, ?)`,
			partID, msgID, sessionID, created+int64(i), data); err != nil {
			t.Fatalf("add part: %v", err)
		}
	}
}

func TestOpenCodeDiscoverSkipsChildAndArchived(t *testing.T) {
	root, db := newOpenCodeStore(t)
	archived := int64(1790000000000)
	addSession(t, db, "ses_top", "", "/work/repo", "Top chat", 3000, nil)
	addMessage(t, db, "ses_top", "msg_1", "user", 100, "text:hi")
	addSession(t, db, "ses_child", "ses_top", "/work/repo", "Sub-agent", 4000, nil)
	addMessage(t, db, "ses_child", "msg_2", "user", 200, "text:spawned")
	addSession(t, db, "ses_archived", "", "/work/repo", "Old chat", 5000, &archived)
	addMessage(t, db, "ses_archived", "msg_3", "user", 300, "text:gone")

	files, err := discoverOpenCode(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].nativeID() != "ses_top" {
		t.Fatalf("discover = %+v, want only ses_top", files)
	}
	if files[0].harness != domain.HarnessOpenCode || files[0].size != 1 {
		t.Fatalf("source = %+v", files[0])
	}
}

func TestOpenCodeTranscriptKeepsOnlyUserAssistantText(t *testing.T) {
	root, db := newOpenCodeStore(t)
	addSession(t, db, "ses_abc123", "", "/work/repo", "Fix the parser", 9000, nil)
	// A user turn, then an assistant turn whose reasoning/tool/step parts are
	// provider internals and must not appear as visible messages.
	addMessage(t, db, "ses_abc123", "msg_a", "user", 1000, "text:Fix the parser")
	addMessage(t, db, "ses_abc123", "msg_b", "assistant", 2000, "step-start", "reasoning", "tool", "text:Done, fixed it.", "step-finish")

	got, err := readTranscript(context.Background(), domain.HarnessOpenCode, root, "ses_abc123")
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Fix the parser", "assistant: Done, fixed it.")
	if got.nativeID != "ses_abc123" || got.cwd != "/work/repo" || got.title != "Fix the parser" {
		t.Fatalf("id=%q cwd=%q title=%q", got.nativeID, got.cwd, got.title)
	}
}

func TestOpenCodePlaceholderTitleFallsBackToPrompt(t *testing.T) {
	root, db := newOpenCodeStore(t)
	addSession(t, db, "ses_ph", "", "/work/repo", "New session - 2026-10-27T10:41:48.917Z", 9000, nil)
	addMessage(t, db, "ses_ph", "msg_u", "user", 1000, "text:What does this function do?")
	addMessage(t, db, "ses_ph", "msg_a", "assistant", 2000, "text:It parses input.")

	got, err := readTranscript(context.Background(), domain.HarnessOpenCode, root, "ses_ph")
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "What does this function do?" {
		t.Fatalf("title = %q, want prompt fallback", got.title)
	}
}

func TestOpenCodeTranscriptRejectsNonConversations(t *testing.T) {
	root, db := newOpenCodeStore(t)
	// Assistant-only (no user prompt) and a child session are not conversations.
	addSession(t, db, "ses_noprompt", "", "/work/repo", "", 9000, nil)
	addMessage(t, db, "ses_noprompt", "msg_a", "assistant", 1000, "text:Hello there.")
	addSession(t, db, "ses_sub", "ses_noprompt", "/work/repo", "Sub", 9000, nil)
	addMessage(t, db, "ses_sub", "msg_b", "user", 1000, "text:spawned")

	for _, id := range []string{"ses_noprompt", "ses_sub", "ses_missing"} {
		if _, err := readTranscript(context.Background(), domain.HarnessOpenCode, root, id); err == nil {
			t.Fatalf("%s: expected error, got none", id)
		}
	}
}

func TestOpenCodeMissingDatabaseYieldsNoCandidates(t *testing.T) {
	files, err := discoverOpenCode(context.Background(), t.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("discover = %+v, err = %v", files, err)
	}
}
