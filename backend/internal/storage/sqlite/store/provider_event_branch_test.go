package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// conversation_provider_events_branch_insert gives an archive row the conversation's
// active branch with an UPDATE after the INSERT, but only when the row arrives with an
// empty branch_id. The archive INSERT sets branch_id itself, so the row is written
// once. These tests watch for that UPDATE with an observer trigger.

const observeBranchRewrites = `
CREATE TABLE branch_rewrites (event_id INTEGER NOT NULL);
CREATE TRIGGER observe_branch_rewrites AFTER UPDATE OF branch_id ON conversation_provider_events
BEGIN
    INSERT INTO branch_rewrites (event_id) VALUES (NEW.id);
END;`

// archiveBranchFixture is a Chat session with a conversation on its root branch, and
// a second connection to the same database on which branch_id rewrites are observed.
func archiveBranchFixture(t *testing.T) (*sqlite.Store, *sql.DB, domain.SessionRecord, domain.ConversationRecord) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, s, "branches")

	rec := sampleRecord("branches")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ProviderConversationID = "thread-root"
	rec.Metadata.ControllerGeneration = "generation-root"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	conversation, err := s.CreateConversation(
		ctx, "conversation-branches", domain.ConversationScopeSession,
		"branches", session.ID, testNow,
	)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatalf("open observer connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(observeBranchRewrites); err != nil {
		t.Fatalf("install branch_id observer: %v", err)
	}
	return s, raw, session, conversation
}

func archiveEvent(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID, generation, eventID string) {
	t.Helper()
	if _, err := s.ProjectProviderEvent(context.Background(), conversation, session, generation, eventID, "turn.started", `{}`,
		testNow, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("ProjectProviderEvent %s: %v", eventID, err)
	}
}

func archivedBranch(t *testing.T, raw *sql.DB, eventID string) string {
	t.Helper()
	var branch string
	if err := raw.QueryRow(`SELECT branch_id FROM conversation_provider_events WHERE provider_event_id = ?`,
		eventID).Scan(&branch); err != nil {
		t.Fatalf("read archived branch of %s: %v", eventID, err)
	}
	return branch
}

func branchRewrites(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRow(`SELECT count(*) FROM branch_rewrites`).Scan(&n); err != nil {
		t.Fatalf("count branch_id rewrites: %v", err)
	}
	return n
}

// assertTriggerAgrees inserts a row the way a writer that omits branch_id does, so the
// trigger assigns it, and checks that the observer saw that rewrite and that the
// branch it assigned is the one the archive INSERT chose. It runs last: the control
// row's rewrite is the only one the observer should have recorded.
func assertTriggerAgrees(t *testing.T, raw *sql.DB, session domain.SessionID, conversation, archivedEventID string) {
	t.Helper()
	if got := branchRewrites(t, raw); got != 0 {
		t.Fatalf("the trigger rewrote %d archive rows that were inserted with their branch", got)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_provider_events
		(conversation_id, session_id, provider_event_id, method, payload_json, received_at)
		VALUES (?, ?, 'control', 'turn.started', '{}', ?)`, conversation, session, testNow); err != nil {
		t.Fatalf("insert control row: %v", err)
	}
	if got := branchRewrites(t, raw); got != 1 {
		t.Fatalf("branch_id observer saw %d rewrites for the control row, want 1: it cannot detect the trigger", got)
	}
	if want, got := archivedBranch(t, raw, "control"), archivedBranch(t, raw, archivedEventID); got != want {
		t.Fatalf("archive INSERT chose branch %q, the trigger chooses %q", got, want)
	}
}

func TestArchiveInsertSetsTheRootBranchWithoutARewrite(t *testing.T) {
	s, raw, session, conversation := archiveBranchFixture(t)

	for _, id := range []string{"event-1", "event-2", "event-3"} {
		archiveEvent(t, s, conversation.ID, session.ID, "generation-root", id)
	}

	if got := archivedBranch(t, raw, "event-3"); got != conversation.ActiveBranchID {
		t.Fatalf("archived branch = %q, want the active branch %q", got, conversation.ActiveBranchID)
	}
	assertTriggerAgrees(t, raw, session.ID, conversation.ID, "event-3")
}

func TestArchiveInsertFollowsTheActiveBranchWithoutARewrite(t *testing.T) {
	ctx := context.Background()
	s, raw, session, conversation := archiveBranchFixture(t)
	seedBranchTurns(t, s, session, conversation)
	branch := domain.ConversationBranch{
		ID: "branch-child", ConversationID: conversation.ID, ProviderConversationID: "thread-child",
		ParentBranchID: conversation.ActiveBranchID, ForkAfterTurnID: "turn-1", ReplacedTurnID: "turn-2",
		ForkAfterSequence: 2,
	}
	if err := s.CreateConversationBranch(ctx, branch, testNow); err != nil {
		t.Fatalf("CreateConversationBranch: %v", err)
	}
	archiveEvent(t, s, conversation.ID, session.ID, "generation-root", "on-root")
	activateTestBranch(t, s, session, conversation, branch.ID, "thread-child", "generation-child")
	archiveEvent(t, s, conversation.ID, session.ID, "generation-child", "on-child")

	if got := archivedBranch(t, raw, "on-root"); got != conversation.ActiveBranchID {
		t.Fatalf("event archived before the switch is on %q, want %q", got, conversation.ActiveBranchID)
	}
	if got := archivedBranch(t, raw, "on-child"); got != branch.ID {
		t.Fatalf("event archived after the switch is on %q, want %q", got, branch.ID)
	}
	assertTriggerAgrees(t, raw, session.ID, conversation.ID, "on-child")
}

// An event archived from inside another event's projection takes the nested path,
// which reuses the outer transaction. A batched write archives its events that way.
func TestNestedArchiveInsertsSetTheBranchWithoutARewrite(t *testing.T) {
	ctx := context.Background()
	s, raw, session, conversation := archiveBranchFixture(t)

	_, err := s.ProjectProviderEvent(ctx, conversation.ID, session.ID, "generation-root", "outer", "turn.started", `{}`,
		testNow, func(txCtx context.Context) error {
			for _, id := range []string{"inner-1", "inner-2"} {
				if _, err := s.ProjectProviderEvent(txCtx, conversation.ID, session.ID, "generation-root", id,
					"message.delta", `{}`, testNow, func(context.Context) error { return nil }); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		t.Fatalf("nested ProjectProviderEvent: %v", err)
	}

	for _, id := range []string{"outer", "inner-1", "inner-2"} {
		if got := archivedBranch(t, raw, id); got != conversation.ActiveBranchID {
			t.Fatalf("archived branch of %s = %q, want %q", id, got, conversation.ActiveBranchID)
		}
	}
	assertTriggerAgrees(t, raw, session.ID, conversation.ID, "inner-2")
}

// Replaying native history happens inside the transaction that moves the
// conversation head, so its events must land on the branch that transaction just
// activated, not the one active when it began.
func TestArchiveInsertSeesTheBranchActivatedEarlierInTheSameTransaction(t *testing.T) {
	ctx := context.Background()
	s, raw, session, conversation := archiveBranchFixture(t)
	committedAt := testNow.Add(time.Minute)
	session.Metadata.ProviderConversationID = "thread-fresh"
	session.Metadata.ControllerGeneration = "generation-fresh"
	session.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: committedAt}
	session.UpdatedAt = committedAt
	boundary := domain.ConversationBranch{
		ID: "fresh-provider-boundary", ConversationID: conversation.ID, SessionID: session.ID,
		ProviderConversationID: "thread-fresh", ParentBranchID: conversation.ActiveBranchID,
		ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "fresh-provider-boundary",
		CreatedAt: committedAt,
	}

	err := s.CommitChatSpawnPrepared(ctx, session, boundary, nil, func(txCtx context.Context) error {
		_, err := s.ProjectProviderEvent(txCtx, conversation.ID, session.ID, "generation-fresh", "replayed",
			"turn.started", `{}`, committedAt, func(context.Context) error { return nil })
		return err
	})
	if err != nil {
		t.Fatalf("CommitChatSpawnPrepared: %v", err)
	}

	if got := archivedBranch(t, raw, "replayed"); got != boundary.ID {
		t.Fatalf("replayed event archived on %q, want the branch activated in its transaction, %q", got, boundary.ID)
	}
	assertTriggerAgrees(t, raw, session.ID, conversation.ID, "replayed")
}

// An archive row for a conversation that does not exist fails on its foreign key.
// Setting branch_id in the INSERT must not turn that error into a silently ignored
// row: INSERT OR IGNORE swallows a NOT NULL violation.
func TestArchiveInsertForAMissingConversationStillFails(t *testing.T) {
	ctx := context.Background()
	s, raw, session, _ := archiveBranchFixture(t)

	applied, err := s.ProjectProviderEvent(ctx, "no-such-conversation", session.ID, "generation-root", "orphan",
		"turn.started", `{}`, testNow, func(context.Context) error { return nil })
	if err == nil || applied {
		t.Fatalf("ProjectProviderEvent for a missing conversation = applied %v, err %v; want an error", applied, err)
	}
	var rows int
	if err := raw.QueryRow(`SELECT count(*) FROM conversation_provider_events`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("archive rows after the failed insert = %d, %v; want none", rows, err)
	}
}
