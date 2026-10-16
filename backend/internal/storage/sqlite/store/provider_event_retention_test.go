package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const retentionDay = 24 * time.Hour

// archiveEvent archives one provider event through the production path, with
// the projection a controller would apply, so snapshots read as they do live.
func archiveEvent(
	t *testing.T,
	s *sqlite.Store,
	conversation string,
	session domain.SessionID,
	eventID, method, providerTurnID, item, text string,
	at time.Time,
) {
	t.Helper()
	ctx := context.Background()
	applied, err := s.ProjectProviderEvent(ctx, conversation, session, "gen-1", eventID, method, `{}`, at,
		func(txCtx context.Context) error {
			switch method {
			case "message.delta":
				return s.AppendAssistantDelta(txCtx, conversation, item, providerTurnID, text, item+"-msg", at)
			case "message.completed":
				return s.SettleAssistantMessage(txCtx, conversation, item, providerTurnID, text, item+"-msg", at)
			case "activity.started":
				return s.UpsertActivity(txCtx, conversation, providerTurnID, domain.ConversationActivity{
					ID: item + "-act", Kind: domain.ActivityKindReasoning, Status: domain.ActivityStatusRunning,
					Summary: "Reasoning", ProviderItemID: item,
				}, at)
			case "reasoning.delta":
				_, err := s.AppendActivityStreamedText(txCtx, conversation, item, text, at)
				return err
			case "command.output.delta":
				_, err := s.AppendCommandOutput(txCtx, conversation, item, text, at)
				return err
			case "turn.completed":
				return s.SettleTurn(txCtx, conversation, providerTurnID, domain.TurnStateCompleted, "", at)
			}
			return nil
		})
	if err != nil || !applied {
		t.Fatalf("archive %s %s: applied=%v err=%v", method, eventID, applied, err)
	}
}

func openRetentionTurn(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID, turnID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{
		ID: turnID + "-prompt", Text: "prompt " + turnID, Origin: domain.MessageOriginHuman,
	}, turnID, at)
	if err != nil || !created {
		t.Fatalf("open turn %s: created=%v err=%v", turnID, created, err)
	}
	if err := s.BindTurnToProvider(ctx, turnID, "provider-"+turnID, at); err != nil {
		t.Fatalf("bind turn %s: %v", turnID, err)
	}
}

type archivedRow struct{ method, eventID string }

func archivedRows(t *testing.T, s *sqlite.Store, conversation string) []archivedRow {
	t.Helper()
	events, err := s.ProviderEventsSince(context.Background(), conversation, 0, 1000)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	rows := make([]archivedRow, 0, len(events))
	for _, event := range events {
		rows = append(rows, archivedRow{method: event.Method, eventID: event.ProviderEventID})
	}
	return rows
}

func pruneDeltas(t *testing.T, s *sqlite.Store, settledBefore time.Time, limit int64) int64 {
	t.Helper()
	deleted, err := s.PruneSettledProviderDeltas(context.Background(), settledBefore, limit)
	if err != nil {
		t.Fatalf("prune settled deltas: %v", err)
	}
	return deleted
}

// Only streamed message and reasoning text without a provider event id is
// deleted. Every other row either deduplicates a provider replay (any row with
// an id), carries output no settled event restates (command output), or is the
// settled record itself.
func TestPruneSettledProviderDeltasDeletesOnlyAnonymousTextDeltas(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	now := histClock.Add(40 * retentionDay)
	settledBefore := now.Add(-14 * retentionDay)

	at := histClock
	openRetentionTurn(t, s, conversation, session, "old", at)
	step := func() time.Time { at = at.Add(time.Second); return at }
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-old", "answer", "Hel", step())
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-old", "answer", "lo", step())
	archiveEvent(t, s, conversation, session, "", "activity.started", "provider-old", "thought", "", step())
	archiveEvent(t, s, conversation, session, "", "reasoning.delta", "provider-old", "thought", "thinking", step())
	archiveEvent(t, s, conversation, session, "acp-host:h:1", "message.delta", "provider-old", "aside", "kept", step())
	archiveEvent(t, s, conversation, session, "acp-history:h", "reasoning.delta", "provider-old", "thought", " more", step())
	archiveEvent(t, s, conversation, session, "", "command.output.delta", "provider-old", "thought", "out", step())
	archiveEvent(t, s, conversation, session, "", "message.completed", "provider-old", "answer", "Hello", step())
	archiveEvent(t, s, conversation, session, "turn-done", "turn.completed", "provider-old", "", "", step())

	// Requested and streamed before the cutoff, settled yesterday: older than the
	// window by arrival, but its turn has not been settled for the whole window.
	recent := settledBefore.Add(-retentionDay)
	openRetentionTurn(t, s, conversation, session, "recent", recent)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-recent", "late", "new", recent.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-recent", "", "", now.Add(-retentionDay))

	before, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}

	if deleted := pruneDeltas(t, s, settledBefore, 100); deleted != 3 {
		t.Fatalf("deleted = %d, want the 3 anonymous message and reasoning deltas of the old turn", deleted)
	}
	want := []archivedRow{
		{"activity.started", ""},
		{"message.delta", "acp-host:h:1"},
		{"reasoning.delta", "acp-history:h"},
		{"command.output.delta", ""},
		{"message.completed", ""},
		{"turn.completed", "turn-done"},
		{"message.delta", ""},
		{"turn.completed", ""},
	}
	if got := archivedRows(t, s, conversation); !reflect.DeepEqual(got, want) {
		t.Fatalf("archive after prune =\n%v\nwant\n%v", got, want)
	}
	after, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("snapshot changed by pruning:\nbefore %+v\nafter  %+v", before, after)
	}
	if deleted := pruneDeltas(t, s, settledBefore, 100); deleted != 0 {
		t.Fatalf("second prune deleted %d rows, want 0", deleted)
	}
}

// A turn's deltas stay until that turn, and every turn requested before them in
// the conversation, has been settled for the whole window.
func TestPruneSettledProviderDeltasWaitsForTheWholeWindowAfterSettlement(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	settledBefore := histClock.Add(26 * retentionDay)

	openRetentionTurn(t, s, conversation, session, "first", histClock)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-first", "first-answer", "a", histClock.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-first", "", "", histClock.Add(2*time.Second))

	stuckAt := histClock.Add(time.Hour)
	openRetentionTurn(t, s, conversation, session, "stuck", stuckAt)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-stuck", "stuck-answer", "b", stuckAt.Add(time.Second))

	if deleted := pruneDeltas(t, s, settledBefore, 100); deleted != 1 {
		t.Fatalf("deleted = %d, want only the settled turn's delta", deleted)
	}
	stuckDelta := []archivedRow{{"turn.completed", ""}, {"message.delta", ""}}
	if got := archivedRows(t, s, conversation); !reflect.DeepEqual(got, stuckDelta) {
		t.Fatalf("archive = %v, want the unsettled turn's delta kept: %v", got, stuckDelta)
	}

	// Settled one day inside the window: still kept.
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-stuck", "", "", settledBefore.Add(retentionDay))
	if deleted := pruneDeltas(t, s, settledBefore, 100); deleted != 0 {
		t.Fatalf("deleted %d rows of a turn settled inside the window", deleted)
	}
	// Once that settlement is older than the window, the delta goes.
	if deleted := pruneDeltas(t, s, settledBefore.Add(2*retentionDay), 100); deleted != 1 {
		t.Fatalf("deleted = %d after the window passed, want 1", deleted)
	}
}

// Arrival time bounds the window too: a delta that arrived after the cutoff is
// kept even when no turn is open around it.
func TestPruneSettledProviderDeltasKeepsDeltasYoungerThanTheWindow(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	settledBefore := histClock.Add(26 * retentionDay)
	openRetentionTurn(t, s, conversation, session, "turn", histClock)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", "old", histClock.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-turn", "", "", histClock.Add(2*time.Second))
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", " late", settledBefore.Add(time.Second))

	if deleted := pruneDeltas(t, s, settledBefore, 100); deleted != 1 {
		t.Fatalf("deleted = %d, want only the delta older than the window", deleted)
	}
	want := []archivedRow{{"turn.completed", ""}, {"message.delta", ""}}
	if got := archivedRows(t, s, conversation); !reflect.DeepEqual(got, want) {
		t.Fatalf("archive = %v, want %v", got, want)
	}
}

func TestPruneSettledProviderDeltasDeletesAtMostLimitOldestFirst(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	openRetentionTurn(t, s, conversation, session, "turn", histClock)
	for i := range 5 {
		archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer",
			strings.Repeat("x", i+1), histClock.Add(time.Duration(i+1)*time.Second))
	}
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-turn", "", "", histClock.Add(time.Minute))
	settledBefore := histClock.Add(30 * retentionDay)

	var batches []int64
	for range 4 {
		batches = append(batches, pruneDeltas(t, s, settledBefore, 2))
	}
	if !slices.Equal(batches, []int64{2, 2, 1, 0}) {
		t.Fatalf("batches = %v, want [2 2 1 0]", batches)
	}
}

// execPlanRecorder explains each statement before running it, so the assertion
// is on the statement sqlc compiled in rather than on a copy of its text.
type execPlanRecorder struct {
	gen.DBTX
	t     *testing.T
	plans []string
}

func (r *execPlanRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.t.Helper()
	rows, err := r.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		r.t.Fatalf("explain query plan: %v", err)
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			r.t.Fatalf("scan query plan row: %v", err)
		}
		r.plans = append(r.plans, detail)
	}
	if err := rows.Close(); err != nil {
		r.t.Fatalf("read query plan: %v", err)
	}
	return r.DBTX.ExecContext(ctx, query, args...)
}

// Each batch holds the single writer, so finding its rows must not read the
// rest of the archive: the candidates come from the partial index, oldest first.
func TestPruneSettledProviderDeltasReadsOnlyTheDeltaIndex(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	_ = sqlitetest.MustOpenAt(t, dataDir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	recorder := &execPlanRecorder{DBTX: raw, t: t}
	if _, err := gen.New(recorder).PruneSettledProviderEventDeltas(ctx, gen.PruneSettledProviderEventDeltasParams{
		SettledBefore: sql.NullTime{Time: histClock, Valid: true},
		BatchLimit:    10,
	}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	plan := strings.Join(recorder.plans, "\n")
	if !strings.Contains(plan, "USING INDEX idx_conversation_provider_events_anonymous_deltas (received_at<?)") {
		t.Errorf("prune does not search the anonymous-delta index:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN delta") || strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
		t.Errorf("prune scans or sorts the archive:\n%s", plan)
	}
}
