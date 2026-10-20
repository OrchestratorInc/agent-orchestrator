package triage

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func setCollectorPaths(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv("AO_RUN_FILE", filepath.Join(dir, "running.json"))
}

func evidenceID(t *testing.T, evidence []Evidence, id string) Evidence {
	t.Helper()
	for _, item := range evidence {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing evidence %q: %#v", id, evidence)
	return Evidence{}
}

func TestCollectKeepsUsefulPartialEvidenceWithoutCreatingDatabase(t *testing.T) {
	dir := t.TempDir()
	setCollectorPaths(t, dir)
	log := "daemon stopped after token=ghp_abcdefghijklmnopqrstuvwxyz123456\n"
	if err := os.WriteFile(filepath.Join(dir, "daemon.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	output := strings.Repeat("startup failure ", 4000) + " api_key=private-value"
	evidence, err := Collect(context.Background(), dir, Origin{Surface: "startup", DaemonState: "error", DaemonCode: "spawn-failed", DaemonOutput: output,
		Machine: json.RawMessage(`{"fetchedAt":"2026-10-19T10:00:00Z","app":{"cpuMeasured":false,"cpuPercent":0},"/Users/alice/private":4}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ao.db")); !os.IsNotExist(err) {
		t.Fatalf("missing database was created: %v", err)
	}
	if item := evidenceID(t, evidence, "daemon-log"); item.Status != "collected" || !strings.Contains(item.Text, "daemon stopped") || strings.Contains(item.Text, "ghp_abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("daemon log was not retained and redacted: %#v", item)
	}
	if item := evidenceID(t, evidence, "daemon-output"); item.Status != "truncated" || len(item.Text) > 24<<10 || strings.Contains(item.Text, "private-value") {
		t.Fatalf("startup output was not bounded and redacted: bytes=%d item=%#v", len(item.Text), item)
	}
	if item := evidenceID(t, evidence, "session-candidates"); item.Status != "unavailable" {
		t.Fatalf("missing database was hidden: %#v", item)
	}
	if item := evidenceID(t, evidence, "machine"); !strings.Contains(string(mustJSON(t, item.Values)), `"cpuPercent":null`) || !strings.Contains(string(mustJSON(t, item.Values)), "2026-10-19T10:00:00Z") {
		t.Fatalf("cached machine values lost their sample time or unknown CPU state: %#v", item)
	}
	if strings.Contains(string(mustJSON(t, evidence)), "/Users/alice/private") {
		t.Fatal("a sensitive map key survived redaction")
	}
	if evidenceID(t, evidence, "origin").Values["daemonState"] != "error" {
		t.Fatal("desktop supplied daemon state was lost")
	}
	for _, item := range evidence {
		if strings.HasPrefix(item.ID, "daemon-health") || strings.HasPrefix(item.ID, "daemon-ready") {
			t.Fatalf("collector unexpectedly probed daemon: %q", item.ID)
		}
	}
}

func TestCollectAutomaticallyReadsSelectedLocalContextAndLeavesDBUnchanged(t *testing.T) {
	dir, workspace, sessionID := triageCollectionFixture(t)
	setCollectorPaths(t, dir)
	before, err := os.ReadFile(filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.log"), []byte("cached failure\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.log"), []byte("2026-10-19T00:00:00Z session="+sessionID+" hook failed token=ghp_abcdefghijklmnopqrstuvwxyz123456\n2026-10-19T00:00:01Z session=other unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	providerDir := filepath.Join(dir, "chat-hosts", sessionID)
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "acp-prompt.journal"), []byte("{\"token\":\"ghp_abcdefghijklmnopqrstuvwxyz123456\",\"text\":\"provider journal\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidence, err := Collect(context.Background(), dir, Origin{SessionID: sessionID, Session: json.RawMessage(mustJSON(t, map[string]any{"id": sessionID, "prompt": "private cached task"}))})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustJSON(t, evidence))
	for _, private := range []string{workspace, "private cached task", "ghp_abcdefghijklmnopqrstuvwxyz123456", "unrelated"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("automatic context leaked %q: %s", private, encoded)
		}
	}
	for _, id := range []string{"retained-session", "session-history", "provider-journal", "provider-archive", "retained-turn", "session-hook-errors"} {
		evidenceID(t, evidence, id)
	}
	if item := evidenceID(t, evidence, "provider-journal"); !strings.Contains(item.Text, "provider journal") || strings.Contains(item.Text, "ghp_abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("selected provider journal was not automatically retained and redacted: %#v", item)
	}
	if !strings.Contains(encoded, "The preview stopped after saving") || !strings.Contains(encoded, "command-output") {
		t.Fatalf("selected conversation and tool activity missing: %s", encoded)
	}
	after, err := os.ReadFile(filepath.Join(dir, "ao.db"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("collection changed database bytes: readErr=%v", err)
	}
}

func TestInvestigationPathsRequireExactLocalRegisteredWorktree(t *testing.T) {
	dir, workspace, sessionID := triageCollectionFixture(t)
	origin := Origin{SessionID: sessionID}
	paths := InvestigationPaths(context.Background(), dir, origin)
	if paths["workspace"] != workspace {
		t.Fatalf("registered root missing: %#v", paths)
	}
	for _, remote := range []Origin{
		{SessionID: sessionID, HostID: "remote"},
		{SessionID: sessionID, Session: json.RawMessage(`{"id":"other"}`)},
		{SessionID: sessionID, Session: json.RawMessage(`{"id":"selected","cloud":{"host":"remote"}}`)},
	} {
		if got := InvestigationPaths(context.Background(), dir, remote); len(got) != 0 {
			t.Fatalf("remote or mismatched session got local paths: %#v", got)
		}
	}
	if got := InvestigationPaths(context.Background(), filepath.Join(t.TempDir(), "missing"), origin); len(got) != 0 {
		t.Fatalf("missing database got paths: %#v", got)
	}
	for _, unsafe := range []string{"relative", string(filepath.Separator), filepath.Join(dir, "sensitive"), filepath.Join(t.TempDir(), "credentials")} {
		if filepath.IsAbs(unsafe) && unsafe != string(filepath.Separator) {
			if err := os.MkdirAll(unsafe, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := validatedWorkspacePath(dir, unsafe); ok {
			t.Fatalf("unsafe operational path accepted: %q", unsafe)
		}
	}
}

func TestRetainedLogRejectsAliasAndBoundsTail(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "private.log")
	if err := os.WriteFile(secret, []byte("should not be read"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "daemon.log")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if item := readRetainedLog(context.Background(), dir, "daemon.log", "daemon-log", 4, ""); item.Status != "unavailable" || strings.Contains(item.Text, "should not be read") {
		t.Fatalf("log alias was followed: %#v", item)
	}
	if err := os.Remove(filepath.Join(dir, "daemon.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.log"), []byte("old line\n"+strings.Repeat("x", 100)+"\nlast line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if item := readRetainedLog(context.Background(), dir, "daemon.log", "daemon-log", 32, ""); item.Status != "truncated" || strings.Contains(item.Text, "old line") || !strings.Contains(item.Text, "last line") || item.Values["sourceModifiedAt"] == nil {
		t.Fatalf("bounded tail was not honest: %#v", item)
	}
}

func triageCollectionFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.UpsertProject(context.Background(), domain.ProjectRecord{ID: "project", Path: workspace, RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(context.Background(), domain.SessionRecord{ID: "selected", ProjectID: "project", Kind: domain.KindWorker,
		Harness: domain.HarnessFake, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now, DisplayName: "Selected", Metadata: domain.SessionMetadata{WorkspacePath: workspace, Prompt: "private task input"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSessionWorktree(context.Background(), domain.SessionWorktreeRecord{SessionID: session.ID, RepoName: domain.RootWorkspaceRepoName, WorktreePath: workspace}); err != nil {
		t.Fatal(err)
	}
	conversation, err := db.CreateConversation(context.Background(), "conversation", domain.ConversationScopeSession, "project", session.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	file := url.URL{Path: filepath.Join(dir, "ao.db")}
	raw, err := sql.Open("sqlite", "file:"+file.EscapedPath()+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`INSERT INTO conversation_turns (id,conversation_id,handled_by_session_id,state,requested_at,error_message)
VALUES ('turn',?,?, 'failed', ?, 'turn failed token=ghp_abcdefghijklmnopqrstuvwxyz123456')`, conversation.ID, session.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_messages (id,conversation_id,turn_id,sequence,role,origin,text,created_at,updated_at)
VALUES ('message',?, 'turn', 1, 'user', 'human', 'The preview stopped after saving api_key=private-value', ?, ?)`, conversation.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_activities (id,conversation_id,turn_id,sequence,kind,status,summary,detail_json,command_output,streamed_text,created_at,updated_at)
VALUES ('activity',?, 'turn', 2, 'command', 'failed', 'preview check', '{"api_key":"private-value"}', 'command-output', '', ?, ?)`, conversation.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_provider_events (conversation_id,session_id,method,payload_json,received_at)
VALUES (?,?, 'tool.call', '{"token":"ghp_abcdefghijklmnopqrstuvwxyz123456","message":"provider error"}', ?)`, conversation.ID, session.ID, now); err != nil {
		t.Fatal(err)
	}
	providerDir := filepath.Join(dir, "chat-hosts", string(session.ID))
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "acp-prompt.journal"), []byte("{\"token\":\"ghp_abcdefghijklmnopqrstuvwxyz123456\",\"text\":\"provider journal\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, workspace, string(session.ID)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
