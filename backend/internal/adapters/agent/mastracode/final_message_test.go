package mastracode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestTerminalTurnReplyReadsNativeHistory(t *testing.T) {
	const thread = "6f5fba9e-b75f-49de-9947-22da631ff0ff"
	database := filepath.Join(t.TempDir(), "history.db")
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE mastra_messages (id TEXT PRIMARY KEY, thread_id TEXT NOT NULL, content TEXT NOT NULL, role TEXT NOT NULL, type TEXT NOT NULL, "createdAt" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, role, content, at string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO mastra_messages VALUES (?, ?, ?, ?, 'v2', ?)`, id, thread, content, role, at); err != nil {
			t.Fatal(err)
		}
	}
	// The daemon finds the database through AO's own session state, never
	// through the hook's environment or payload.
	dataDir, workspace := t.TempDir(), t.TempDir()
	t.Setenv("MASTRA_DB_PATH", filepath.Join(t.TempDir(), "decoy.db"))
	if err := os.MkdirAll(configPath(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeNativeState(dataDir, "mc-1", workspace, nativeState{Owner: sentinel, Profile: t.TempDir(), Database: database}); err != nil {
		t.Fatal(err)
	}
	reply := func(session domain.SessionID, nativeID string) string {
		return (&Plugin{}).TerminalTurnReply(context.Background(), ports.TerminalReplyConfig{DataDir: dataDir, SessionID: session, NativeSessionID: nativeID})
	}

	insert("1", "signal", `{"format":2,"parts":[{"type":"text","text":"first task"}]}`, "2026-11-18T04:30:39.025Z")
	insert("2", "assistant", `{"format":2,"parts":[{"type":"reasoning","reasoning":""},{"type":"text","text":"Let me check."},{"type":"tool-invocation"},{"type":"text","text":"FIRST-OK"}]}`, "2026-11-18T04:30:54.112Z")
	if got := reply("mc-1", thread); got != "FIRST-OK" {
		t.Fatalf("final answer = %q, want the text after the last tool step", got)
	}

	// The next turn's reply is not persisted yet: never report the previous one.
	insert("3", "signal", `{"format":2,"parts":[{"type":"text","text":"second task"}]}`, "2026-11-18T04:31:10.000Z")
	if got := reply("mc-1", thread); got != "" {
		t.Fatalf("unpersisted reply reported %q", got)
	}
	// A resumed TUI's Stop names no thread (AO drops "session-init").
	insert("4", "assistant", `{"format":2,"parts":[{"type":"text","text":"RESUMED-OK"}]}`, "2026-11-18T04:31:20.000Z")
	if got := reply("mc-1", ""); got != "RESUMED-OK" {
		t.Fatalf("resumed final answer = %q", got)
	}
	if got := reply("mc-1", "not-a-thread"); got != "" {
		t.Fatalf("malformed thread read %q", got)
	}
	for _, session := range []domain.SessionID{"mc-2", "../mc-1", ""} {
		if got := reply(session, thread); got != "" {
			t.Fatalf("session %q without AO native state read %q", session, got)
		}
	}
}
