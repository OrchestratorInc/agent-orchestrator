package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func triageDiagnosticFixture(t *testing.T) (*sqlite.Store, *sqlite.Store, *sql.DB, domain.SessionRecord) {
	t.Helper()
	dir := t.TempDir()
	writable, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writable.Close() })
	seedProject(t, writable, "triage")
	record := sampleRecord("triage")
	record.Mode = domain.SessionModeChat
	record.ProvisionState = domain.SessionProvisionFailed
	record.ProvisionError = strings.Repeat("é", 5000)
	record.Metadata.Prompt = strings.Repeat("private task input ", 10000)
	record.Metadata.WorkspacePath = "/private/selected/workspace"
	selected, err := writable.CreateSession(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	file := url.URL{Path: filepath.Join(dir, "ao.db")}
	raw, err := sql.Open("sqlite", "file:"+file.EscapedPath()+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	readOnly, err := sqlite.OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readOnly.Close() })
	return writable, readOnly, raw, selected
}

func snapshotPart(t *testing.T, db *sqlite.Store, id domain.SessionID, kind string) []map[string]any {
	t.Helper()
	for _, part := range db.ReadTriageSnapshot(context.Background(), id) {
		if part.Kind != kind {
			continue
		}
		if part.Err != nil {
			t.Fatal(part.Err)
		}
		entries := make([]map[string]any, 0, len(part.Entries))
		for _, raw := range part.Entries {
			var entry map[string]any
			if err := json.Unmarshal(raw, &entry); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, entry)
		}
		return entries
	}
	t.Fatalf("missing snapshot part %s", kind)
	return nil
}

func TestTriageSnapshotBoundsPrivateFieldsAndCompleteOperationalPaths(t *testing.T) {
	writable, readOnly, raw, selected := triageDiagnosticFixture(t)
	if _, err := raw.Exec(`UPDATE sessions SET updated_at=? WHERE id=?`, strings.Repeat("timestamp", 2000), selected.ID); err != nil {
		t.Fatal(err)
	}
	entries := snapshotPart(t, readOnly, selected.ID, "retained-session")
	if len(entries) != 1 {
		t.Fatal("selected session missing")
	}
	facts := entries[0]
	if len(facts["provisionError"].(string)) != 4096 || len(facts["updatedAt"].(string)) != 64 {
		t.Fatal("SQL did not bound fields before returning")
	}
	encoded, _ := json.Marshal(facts)
	if strings.Contains(string(encoded), "private task input") || facts["harness"] != string(selected.Harness) {
		t.Fatalf("snapshot included private prompt or wrong harness: %s", encoded)
	}
	for n := range 12 {
		name, path := fmt.Sprintf("repo-%02d", n), fmt.Sprintf("/worktrees/repo-%02d", n)
		if n == 0 {
			name = domain.RootWorkspaceRepoName
			path = strings.Repeat("/long", 2000)
		}
		if err := writable.UpsertSessionWorktree(context.Background(), domain.SessionWorktreeRecord{SessionID: selected.ID, RepoName: name, WorktreePath: path}); err != nil {
			t.Fatal(err)
		}
	}
	roots, err := readOnly.ReadTriageWorkspaceRoots(context.Background(), selected.ID)
	if err != nil || len(roots) != 8 {
		t.Fatalf("root bound: %#v %v", roots, err)
	}
	for _, root := range roots {
		if root.Name == domain.RootWorkspaceRepoName || len(root.Path) > 4096 {
			t.Fatal("partial path became native operational input")
		}
	}
	if entries := snapshotPart(t, readOnly, "absent", "retained-session"); len(entries) != 0 {
		t.Fatal("missing session fabricated")
	}
	candidates := snapshotPart(t, readOnly, "", "session-candidates")
	if len(candidates) != 1 || candidates[0]["id"] != string(selected.ID) {
		t.Fatal("candidate discovery lost exact session identity")
	}
}

func TestTriageSnapshotHistoryAndProviderEventsKeepExactOwnerAndBounds(t *testing.T) {
	writable, readOnly, raw, selected := triageDiagnosticFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	other, err := writable.CreateSession(ctx, sampleRecord("triage"))
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := writable.CreateConversation(ctx, "shared", domain.ConversationScopeProject, "triage", selected.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range []struct {
		id    string
		owner domain.SessionID
	}{{"selected", selected.ID}, {"other", other.ID}} {
		if _, err := raw.Exec(`INSERT INTO conversation_turns (id,conversation_id,handled_by_session_id,state,error_message,requested_at) VALUES (?,?,?,'failed',?,?)`, turn.id, conversation.ID, turn.owner, strings.Repeat(turn.id+" failure ", 2000), now); err != nil {
			t.Fatal(err)
		}
	}
	for n := range 18 {
		if _, err := raw.Exec(`INSERT INTO conversation_messages (id,conversation_id,turn_id,sequence,role,origin,text,created_at,updated_at) VALUES (?,?,'selected',?,'user','human',?,?,?)`, fmt.Sprintf("message-%d", n), conversation.ID, n+1, strings.Repeat("z", 20000), now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO conversation_provider_events (conversation_id,session_id,method,payload_json,received_at) VALUES (?,?,'tool.call',?,?)`, conversation.ID, selected.ID, strings.Repeat("z", 20000), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO conversation_activities (id,conversation_id,turn_id,sequence,kind,status,summary,detail_json,command_output,created_at,updated_at) VALUES ('tool',?,'selected',19,'command','failed','preview check','{"command":"curl preview"}',?,?,?)`, conversation.ID, strings.Repeat("output", 1000), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_messages (id,conversation_id,turn_id,sequence,role,origin,text,created_at,updated_at) VALUES ('private-other',?,'other',99,'user','human','unrelated private task',?,?)`, conversation.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_provider_events (conversation_id,session_id,method,payload_json,received_at) VALUES (?,?,'private-other','{}',?)`, conversation.ID, other.ID, now); err != nil {
		t.Fatal(err)
	}
	history := snapshotPart(t, readOnly, selected.ID, "session-history")
	if len(history) != 6 || history[0]["kind"] != "command" || len(history[0]["output"].(string)) != 1024 || history[1]["sequence"] != float64(18) || len(history[1]["text"].(string)) != 2048 {
		t.Fatalf("bounded tool/history snapshot wrong: %#v", history)
	}
	events := snapshotPart(t, readOnly, selected.ID, "provider-archive")
	if len(events) != 4 || events[0]["method"] != "tool.call" || len(events[0]["payload"].(string)) != 2048 {
		t.Fatal("provider bounds or ownership lost")
	}
	turn := snapshotPart(t, readOnly, selected.ID, "retained-turn")
	if len(turn) != 1 || !strings.HasPrefix(turn[0]["error"].(string), "selected failure") || len(turn[0]["error"].(string)) != 4096 {
		t.Fatal("latest owned turn bounds lost")
	}
	encoded, _ := json.Marshal(history)
	if strings.Contains(string(encoded), "unrelated private") {
		t.Fatal("shared conversation crossed turn ownership")
	}
}

func TestTriageSnapshotKeepsOtherPartsWhenOneTableIsUnavailable(t *testing.T) {
	_, readOnly, raw, selected := triageDiagnosticFixture(t)
	if _, err := raw.Exec(`ALTER TABLE conversation_provider_events RENAME TO unavailable_provider_events`); err != nil {
		t.Fatal(err)
	}
	failed, kept := false, false
	for _, part := range readOnly.ReadTriageSnapshot(context.Background(), selected.ID) {
		if part.Kind == "provider-archive" {
			failed = part.Err != nil
		}
		if part.Kind == "retained-session" {
			kept = part.Err == nil && len(part.Entries) == 1
		}
	}
	if !failed || !kept {
		t.Fatal("one unavailable source erased the partial snapshot")
	}
}
