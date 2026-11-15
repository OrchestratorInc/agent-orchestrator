package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestDeleteTerminatedSessionRemovesRecordAndNeverReusesItsID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	older := terminalSession(t, s, "del", 0)
	newest := terminalSession(t, s, "del", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: newest.ID, WorkspaceDisposition: domain.DispositionRemoved})
	if _, err := s.CreateConversation(ctx, "conv-newest", domain.ConversationScopeSession, "del", newest.ID, time.Now()); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	before, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteTerminatedSession(ctx, newest.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v; want deleted", deleted, err)
	}
	if _, ok, err := s.GetSession(ctx, newest.ID); err != nil || ok {
		t.Fatalf("session still present: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetSessionCleanupFacts(ctx, newest.ID); err != nil || ok {
		t.Fatalf("cleanup facts survived the session: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetSession(ctx, older.ID); err != nil || !ok {
		t.Fatalf("unrelated session removed: ok=%v err=%v", ok, err)
	}

	events, err := s.EventsAfter(ctx, before, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != cdc.EventSessionUpdated || events[0].SessionID != "" || events[0].ProjectID != "del" {
		t.Fatalf("events = %+v, want one project-level session_updated", events)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload["deletedSessionId"] != string(newest.ID) {
		t.Fatalf("payload = %s (%v), want the deleted session id", events[0].Payload, err)
	}

	next, err := s.CreateSession(ctx, sampleRecord("del"))
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == newest.ID {
		t.Fatalf("new session reused deleted id %s", next.ID)
	}
}

// A session's usage events cascade away with it, but its spend must stay in the
// agent account usage view, attributed to the same accounts and days. Two
// deleted sessions on the same account, day and model add up.
func TestDeleteTerminatedSessionKeepsAccountUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	first := seedUsageSession(t, s, domain.HarnessClaudeCode)
	second := sampleRecord("usage")
	second.Harness = domain.HarnessClaudeCode
	second, err := s.CreateSession(ctx, second)
	mustNoError(t, err)
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/acct-b", Email: "b@example.com", CreatedAt: t0,
	}))
	// first moves from the device login to acct-b after a day; second stays on
	// the device login.
	mustNoError(t, s.BindSessionAgentAccount(ctx, first.ID, domain.AgentAccountProviderClaude, "device-claude", t0))
	mustNoError(t, s.BindSessionAgentAccount(ctx, first.ID, domain.AgentAccountProviderClaude, "acct-b", t0.Add(24*time.Hour)))
	for i, sess := range []domain.SessionRecord{first, second} {
		binding := mustUpsertUsageBinding(t, s, sess, t0, domain.UsageBindingRecord{
			NativeRootID: "root", InitialModelID: "claude-x", State: domain.UsageBindingActive,
		})
		source := mustInsertUsageSource(t, s, t0, domain.UsageSourceRecord{
			BindingID: binding.ID, Kind: domain.UsageSourceClaudeMain, NativeSessionID: "native",
			ArtifactPath: fmt.Sprintf("/tmp/claude/%d.jsonl", i), FileIdentity: "dev:ino", State: domain.UsageSourcePending,
		})
		before := anthropicUsageEvent(fmt.Sprintf("before-%d", i), 10, 0, 0, 5)
		before.CreatedAt = t0.Add(time.Hour)
		after := anthropicUsageEvent(fmt.Sprintf("after-%d", i), 20, 0, 0, 7)
		after.CreatedAt = t0.Add(25 * time.Hour)
		mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
			ByteOffset: 100, State: domain.UsageSourceActive, ParserStateJSON: `{}`, UpdatedAt: t0,
		}, []domain.ModelUsageEvent{before, after}))
	}
	totals := func() map[string]domain.AgentAccountUsage {
		t.Helper()
		rows, err := s.AgentAccountUsageSince(ctx, t0.Truncate(24*time.Hour))
		mustNoError(t, err)
		out := map[string]domain.AgentAccountUsage{}
		for _, row := range rows {
			key := row.AccountID + "|" + row.Day + "|" + row.Model
			sum := out[key]
			sum.InputTokens += row.InputTokens
			sum.OutputTokens += row.OutputTokens
			sum.CostNanos += row.CostNanos
			out[key] = sum
		}
		return out
	}
	want := totals()
	if len(want) != 3 {
		t.Fatalf("seeded usage = %+v, want device-claude on both days and acct-b on the second", want)
	}

	for _, sess := range []domain.SessionRecord{first, second} {
		sess.IsTerminated = true
		mustNoError(t, s.UpdateSession(ctx, sess))
		deleted, err := s.DeleteTerminatedSession(ctx, sess.ID)
		if err != nil || !deleted {
			t.Fatalf("delete %s = %v, %v", sess.ID, deleted, err)
		}
		if got := totals(); !reflect.DeepEqual(got, want) {
			t.Fatalf("after deleting %s usage = %+v, want %+v", sess.ID, got, want)
		}
	}
}

func TestDeleteTerminatedSessionRefusesLiveSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	live, err := s.CreateSession(ctx, sampleRecord("del"))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteTerminatedSession(ctx, live.ID)
	if err != nil || deleted {
		t.Fatalf("delete live = %v, %v; want refused", deleted, err)
	}
	if deleted, err := s.DeleteTerminatedSession(ctx, "del-404"); err != nil || deleted {
		t.Fatalf("delete missing = %v, %v; want not deleted", deleted, err)
	}
	if _, ok, err := s.GetSession(ctx, live.ID); err != nil || !ok {
		t.Fatalf("live session removed: ok=%v err=%v", ok, err)
	}
}

// A project orchestrator conversation outlives each orchestrator session;
// deleting one would cascade its turns out of that shared history.
func TestDeleteTerminatedSessionRefusesSharedConversationHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	orchestrator := terminalSession(t, s, "del", 0)
	now := time.Now()
	conversation, err := s.CreateConversation(ctx, "conv-project", domain.ConversationScopeProject, "del", orchestrator.ID, now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := s.AdoptProviderTurn(ctx, conversation.ID, orchestrator.ID, "gen-1", "turn-1", "provider-turn-1", now); err != nil {
		t.Fatalf("adopt turn: %v", err)
	}

	deleted, err := s.DeleteTerminatedSession(ctx, orchestrator.ID)
	if !errors.Is(err, domain.ErrSessionHistoryShared) || deleted {
		t.Fatalf("delete = %v, %v; want ErrSessionHistoryShared", deleted, err)
	}
	if has, err := s.HasConversationTurns(ctx, conversation.ID); err != nil || !has {
		t.Fatalf("shared turns lost: has=%v err=%v", has, err)
	}
}

func openRawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

func countRows(t *testing.T, raw *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := raw.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteTerminatedSessionWithEditedAndRetriedTurns(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "del")
	sess := terminalSession(t, s, "del", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: sess.ID, WorkspaceDisposition: domain.DispositionRemoved})
	now := time.Now().UTC()
	if _, err := s.CreateConversation(ctx, "conv-edited", domain.ConversationScopeSession, "del", sess.ID, now); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	raw := openRawDB(t, dir)
	for _, q := range []string{
		`INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, state, requested_at) VALUES ('turn-1', 'conv-edited', ?1, 'failed', ?2)`,
		`INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, state, requested_at, retry_of_turn_id) VALUES ('turn-2', 'conv-edited', ?1, 'completed', ?2, 'turn-1')`,
		`INSERT INTO conversation_branches (id, conversation_id, session_id, created_at) VALUES ('branch-1', 'conv-edited', ?1, ?2)`,
		`INSERT INTO conversation_branches (id, conversation_id, session_id, parent_branch_id, fork_after_turn_id, replaced_turn_id, created_at) VALUES ('branch-2', 'conv-edited', ?1, 'branch-1', 'turn-1', 'turn-2', ?2)`,
		`INSERT INTO conversation_branches (id, conversation_id, session_id, parent_branch_id, created_at) VALUES ('branch-3', 'conv-edited', ?1, 'branch-2', ?2)`,
	} {
		if _, err := raw.ExecContext(ctx, q, sess.ID, now); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	deleted, err := s.DeleteTerminatedSession(ctx, sess.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v; want deleted", deleted, err)
	}
	if n := countRows(t, raw, `SELECT count(*) FROM conversation_branches WHERE conversation_id = 'conv-edited'`); n != 0 {
		t.Fatalf("%d branches left, want none", n)
	}
}

func TestDeleteTerminatedSessionClearsHistoryInBatches(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "del")
	sess := terminalSession(t, s, "del", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: sess.ID, WorkspaceDisposition: domain.DispositionRemoved})
	live, err := s.CreateSession(ctx, sampleRecord("del"))
	mustNoError(t, err)
	now := time.Now().UTC()
	raw := openRawDB(t, dir)
	seed := func(id domain.SessionID, conversation string, events, activities int) {
		t.Helper()
		if _, err := s.CreateConversation(ctx, conversation, domain.ConversationScopeSession, "del", id, now); err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		if _, err := raw.ExecContext(ctx, `
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?3)
INSERT INTO conversation_provider_events (conversation_id, session_id, method, payload_json, received_at)
SELECT ?1, ?2, 'message.delta', '{}', ?4 FROM n`, conversation, id, events, now); err != nil {
			t.Fatalf("seed provider events: %v", err)
		}
		if _, err := raw.ExecContext(ctx, `
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?2)
INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, created_at, updated_at)
SELECT ?1 || '-activity-' || i, ?1, i, 'command', 'completed', ?3, ?3 FROM n`, conversation, activities, now); err != nil {
			t.Fatalf("seed activities: %v", err)
		}
	}
	seed(sess.ID, "conv-archived", 2501, 1001)
	seed(live.ID, "conv-live", 30, 20)

	if deleted, err := s.DeleteTerminatedSession(ctx, live.ID); err != nil || deleted {
		t.Fatalf("delete live = %v, %v; want refused", deleted, err)
	}
	if n := countRows(t, raw, `SELECT count(*) FROM conversation_provider_events WHERE session_id = ?`, live.ID); n != 30 {
		t.Fatalf("refused delete left %d provider events, want 30", n)
	}
	if n := countRows(t, raw, `SELECT count(*) FROM conversation_activities WHERE conversation_id = 'conv-live'`); n != 20 {
		t.Fatalf("refused delete left %d activities, want 20", n)
	}

	deleted, err := s.DeleteTerminatedSession(ctx, sess.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v; want deleted", deleted, err)
	}
	if n := countRows(t, raw, `SELECT count(*) FROM conversation_provider_events WHERE session_id = ?`, sess.ID); n != 0 {
		t.Fatalf("%d provider events left, want none", n)
	}
	if n := countRows(t, raw, `SELECT count(*) FROM conversation_activities WHERE conversation_id = 'conv-archived'`); n != 0 {
		t.Fatalf("%d activities left, want none", n)
	}
}

// terminated_at follows is_terminated however the row is written. Retention
// lists only archived sessions with nothing left on disk, each with when it was
// archived, or its last update for a row archived before terminated_at existed.
func TestReleasedArchivedSessionsCarryWhenTheyWereArchived(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "ret")
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	terminatedAt := func(id domain.SessionID) sql.NullString {
		t.Helper()
		var at sql.NullString
		if err := raw.QueryRowContext(ctx, `SELECT terminated_at FROM sessions WHERE id = ?`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}

	released := terminalSession(t, s, "ret", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: released.ID, WorkspaceDisposition: domain.DispositionRemoved})
	unreleased := terminalSession(t, s, "ret", 0)
	noWorkspace := sampleRecord("ret")
	noWorkspace.Metadata.WorkspacePath = ""
	noWorkspace, err = s.CreateSession(ctx, noWorkspace)
	mustNoError(t, err)
	noWorkspace.IsTerminated = true
	mustNoError(t, s.UpdateSession(ctx, noWorkspace))
	legacy := terminalSession(t, s, "ret", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: legacy.ID, WorkspaceDisposition: domain.DispositionNotApplicable})
	if _, err := raw.ExecContext(ctx, `UPDATE sessions SET terminated_at = NULL WHERE id = ?`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if !terminatedAt(released.ID).Valid {
		t.Fatal("archiving did not set terminated_at")
	}
	restored := terminalSession(t, s, "ret", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: restored.ID, WorkspaceDisposition: domain.DispositionRemoved})
	restored.IsTerminated = false
	mustNoError(t, s.UpdateSession(ctx, restored))
	if terminatedAt(restored.ID).Valid {
		t.Fatal("restoring did not clear terminated_at")
	}

	got, err := s.ListReleasedArchivedSessions(ctx)
	mustNoError(t, err)
	byID := map[domain.SessionID]time.Time{}
	for _, sess := range got {
		byID[sess.ID] = sess.ArchivedAt
	}
	if len(byID) != 3 || byID[unreleased.ID] != (time.Time{}) || byID[restored.ID] != (time.Time{}) {
		t.Fatalf("listed = %+v, want released, no-workspace and legacy only", got)
	}
	for _, id := range []domain.SessionID{released.ID, noWorkspace.ID} {
		if at := byID[id]; time.Since(at) > time.Minute || time.Until(at) > time.Minute {
			t.Fatalf("%s archived at %v, want about now", id, at)
		}
	}
	if !byID[legacy.ID].Equal(legacy.UpdatedAt) {
		t.Fatalf("legacy archived at %v, want its last update %v", byID[legacy.ID], legacy.UpdatedAt)
	}
}

// Retention leaves out the sessions delete refuses for shared history: an
// earlier orchestrator whose turns stay in the project conversation, and the
// one that conversation now belongs to. Otherwise every pass would retry them.
// A session's own conversation does not hold it back.
func TestReleasedArchivedSessionsLeaveOutSharedConversationHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ret")
	now := time.Now()
	released := func() domain.SessionRecord {
		t.Helper()
		sess := terminalSession(t, s, "ret", 0)
		upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: sess.ID, WorkspaceDisposition: domain.DispositionRemoved})
		return sess
	}
	previous := released()
	current := released()
	own := released()
	project, err := s.CreateConversation(ctx, "conv-project", domain.ConversationScopeProject, "ret", previous.ID, now)
	mustNoError(t, err)
	mustNoError(t, s.AdoptProviderTurn(ctx, project.ID, previous.ID, "gen-1", "turn-1", "provider-turn-1", now))
	_, err = s.CreateConversation(ctx, project.ID, domain.ConversationScopeProject, "ret", current.ID, now)
	mustNoError(t, err)
	ownConversation, err := s.CreateConversation(ctx, "conv-own", domain.ConversationScopeSession, "ret", own.ID, now)
	mustNoError(t, err)
	mustNoError(t, s.AdoptProviderTurn(ctx, ownConversation.ID, own.ID, "gen-1", "turn-2", "provider-turn-2", now))

	got, err := s.ListReleasedArchivedSessions(ctx)
	mustNoError(t, err)
	if len(got) != 1 || got[0].ID != own.ID {
		t.Fatalf("listed = %+v, want only %s", got, own.ID)
	}
	for _, id := range []domain.SessionID{previous.ID, current.ID} {
		if deleted, err := s.DeleteTerminatedSession(ctx, id); !errors.Is(err, domain.ErrSessionHistoryShared) || deleted {
			t.Fatalf("delete %s = %v, %v; want ErrSessionHistoryShared", id, deleted, err)
		}
	}
}
