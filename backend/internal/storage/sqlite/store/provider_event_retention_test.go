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

const (
	retentionDay    = 24 * time.Hour
	retentionWindow = 2000
	retentionGrace  = retentionDay // chat.DefaultArchiveRetentionGrace
)

// retentionFixture is conversationFixture on a known data dir, so a test can
// open the same database again.
func retentionFixture(t *testing.T, dir string) (*sqlite.Store, domain.SessionID, string) {
	t.Helper()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "hist")
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	conversation, err := s.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, "hist", session.ID, histClock)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, session.ID, "gen-1"); err != nil {
		t.Fatalf("claim controller generation: %v", err)
	}
	return s, session.ID, conversation.ID
}

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

func pruneStep(t *testing.T, s *sqlite.Store, settledBefore time.Time, window, limit int64) (int64, bool) {
	t.Helper()
	deleted, caughtUp, _, err := s.PruneSettledProviderDeltas(context.Background(), settledBefore, retentionGrace, window, limit)
	if err != nil {
		t.Fatalf("prune settled deltas: %v", err)
	}
	return deleted, caughtUp
}

// pruneAll steps the cursor until it has caught up and returns the rows deleted.
func pruneAll(t *testing.T, s *sqlite.Store, settledBefore time.Time) int64 {
	t.Helper()
	var total int64
	for range 100 {
		deleted, caughtUp := pruneStep(t, s, settledBefore, retentionWindow, 100)
		total += deleted
		if caughtUp {
			return total
		}
	}
	t.Fatal("prune never caught up")
	return 0
}

// Only streamed message text without a provider event id is deleted. A row with
// an id deduplicates a provider replay; reasoning and command output are not
// fully restated by their settled events; every other row is the settled record.
func TestPruneSettledProviderDeltasDeletesOnlyAnonymousMessageDeltas(t *testing.T) {
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

	// A turn longer than the grace: requested and streamed well before the cutoff,
	// settled yesterday. The cursor visits its delta while the turn is still inside
	// the window and passes it.
	recent := settledBefore.Add(-3 * retentionDay)
	openRetentionTurn(t, s, conversation, session, "recent", recent)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-recent", "late", "new", recent.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-recent", "", "", now.Add(-retentionDay))

	before, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}

	if deleted := pruneAll(t, s, settledBefore); deleted != 2 {
		t.Fatalf("deleted = %d, want the 2 anonymous message deltas of the old turn", deleted)
	}
	want := []archivedRow{
		{"activity.started", ""},
		{"reasoning.delta", ""},
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
	if deleted := pruneAll(t, s, settledBefore); deleted != 0 {
		t.Fatalf("second prune deleted %d rows, want 0", deleted)
	}
}

// A delta whose turn is still open when the cursor passes it stays for good,
// across a restart and after the turn settles: the safe direction.
func TestPruneSettledProviderDeltasKeepsADeltaPassedWhileItsTurnWasOpen(t *testing.T) {
	dir := t.TempDir()
	s, session, conversation := retentionFixture(t, dir)
	settledBefore := histClock.Add(26 * retentionDay)

	openRetentionTurn(t, s, conversation, session, "first", histClock)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-first", "first-answer", "a", histClock.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-first", "", "", histClock.Add(2*time.Second))

	stuckAt := histClock.Add(time.Hour)
	openRetentionTurn(t, s, conversation, session, "stuck", stuckAt)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-stuck", "stuck-answer", "b", stuckAt.Add(time.Second))

	if deleted := pruneAll(t, s, settledBefore); deleted != 1 {
		t.Fatalf("deleted = %d, want only the settled turn's delta", deleted)
	}
	kept := []archivedRow{{"turn.completed", ""}, {"message.delta", ""}}
	if got := archivedRows(t, s, conversation); !reflect.DeepEqual(got, kept) {
		t.Fatalf("archive = %v, want the open turn's delta kept: %v", got, kept)
	}

	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-stuck", "", "", settledBefore.Add(retentionDay))
	reopened, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if deleted := pruneAll(t, reopened, settledBefore.Add(30*retentionDay)); deleted != 0 {
		t.Fatalf("deleted %d rows the persisted cursor had already passed", deleted)
	}
	kept = append(kept, archivedRow{"turn.completed", ""})
	if got := archivedRows(t, reopened, conversation); !reflect.DeepEqual(got, kept) {
		t.Fatalf("archive = %v, want %v", got, kept)
	}
}

// The cursor stops at the first delta received within the grace before the
// cutoff, and resumes there once the cutoff has moved on, so nothing behind it
// is skipped.
func TestPruneSettledProviderDeltasStopsAtTheGraceBoundaryAndResumes(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	settledBefore := histClock.Add(26 * retentionDay)
	openRetentionTurn(t, s, conversation, session, "turn", histClock)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", "old", histClock.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-turn", "", "", histClock.Add(2*time.Second))
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", " in grace", settledBefore.Add(-12*time.Hour))
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", " young", settledBefore.Add(time.Second))

	deleted, caughtUp := pruneStep(t, s, settledBefore, retentionWindow, 100)
	if deleted != 1 || !caughtUp {
		t.Fatalf("first step = (%d, %v), want (1, true): stop at the delta inside the grace", deleted, caughtUp)
	}
	if got := pruneAll(t, s, settledBefore); got != 0 {
		t.Fatalf("deleted %d rows inside the grace", got)
	}
	if got := pruneAll(t, s, settledBefore.Add(13*time.Hour)); got != 1 {
		t.Fatalf("deleted %d once the grace passed the first delta, want 1", got)
	}
	if got := pruneAll(t, s, settledBefore.Add(2*retentionDay)); got != 1 {
		t.Fatalf("deleted %d once the grace passed the young delta, want 1", got)
	}
	if got := archivedRows(t, s, conversation); !reflect.DeepEqual(got, []archivedRow{{"turn.completed", ""}}) {
		t.Fatalf("archive = %v", got)
	}
}

// Steady state: a pass every 15 minutes with the cutoff 14 days back. A turn
// that settles 30 minutes after its deltas must still have them pruned once the
// deltas and the settlement are both past the cutoff; a turn that never settles
// keeps its delta.
func TestPruneSettledProviderDeltasSteadyStatePrunesATurnThatSettlesAfterItsDeltas(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	openRetentionTurn(t, s, conversation, session, "slow", histClock)
	for i := 1; i <= 3; i++ {
		archiveEvent(t, s, conversation, session, "", "message.delta", "provider-slow", "slow-answer", "x",
			histClock.Add(time.Duration(i)*time.Minute))
	}
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-slow", "", "", histClock.Add(30*time.Minute))
	openAt := histClock.Add(time.Hour)
	openRetentionTurn(t, s, conversation, session, "open", openAt)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-open", "open-answer", "y", openAt.Add(time.Minute))

	for now := histClock; !now.After(histClock.Add(16 * retentionDay)); now = now.Add(15 * time.Minute) {
		pruneAll(t, s, now.Add(-14*retentionDay))
	}
	want := []archivedRow{{"turn.completed", ""}, {"message.delta", ""}}
	if got := archivedRows(t, s, conversation); !reflect.DeepEqual(got, want) {
		t.Fatalf("archive after 16 days of passes = %v, want the slow turn's deltas pruned and the open turn's delta kept: %v", got, want)
	}
}

// The cursor never moves past the newest archive row, so rows written after it
// caught up are still visited.
func TestPruneSettledProviderDeltasVisitsRowsAddedAfterItCaughtUp(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	settledBefore := histClock.Add(26 * retentionDay)
	openRetentionTurn(t, s, conversation, session, "turn", histClock)
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", "a", histClock.Add(time.Second))
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-turn", "", "", histClock.Add(2*time.Second))
	if got := pruneAll(t, s, settledBefore); got != 1 {
		t.Fatalf("first prune deleted %d, want 1", got)
	}
	archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer", "b", histClock.Add(3*time.Second))
	if got := pruneAll(t, s, settledBefore); got != 1 {
		t.Fatalf("prune after new rows deleted %d, want 1", got)
	}
}

// Each call deletes at most limit rows and visits at most window ids.
func TestPruneSettledProviderDeltasBoundsEachStep(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	openRetentionTurn(t, s, conversation, session, "turn", histClock)
	for i := range 5 {
		archiveEvent(t, s, conversation, session, "", "message.delta", "provider-turn", "answer",
			strings.Repeat("x", i+1), histClock.Add(time.Duration(i+1)*time.Second))
	}
	archiveEvent(t, s, conversation, session, "", "turn.completed", "provider-turn", "", "", histClock.Add(time.Minute))
	settledBefore := histClock.Add(30 * retentionDay)

	type step struct {
		deleted  int64
		caughtUp bool
	}
	var byLimit []step
	for range 3 {
		deleted, caughtUp := pruneStep(t, s, settledBefore, retentionWindow, 2)
		byLimit = append(byLimit, step{deleted, caughtUp})
	}
	if want := []step{{2, false}, {2, false}, {1, true}}; !slices.Equal(byLimit, want) {
		t.Fatalf("steps with limit 2 = %v, want %v", byLimit, want)
	}

	s2, session2, conversation2 := conversationFixture(t)
	openRetentionTurn(t, s2, conversation2, session2, "turn", histClock)
	for i := range 5 {
		archiveEvent(t, s2, conversation2, session2, "", "message.delta", "provider-turn", "answer",
			strings.Repeat("x", i+1), histClock.Add(time.Duration(i+1)*time.Second))
	}
	archiveEvent(t, s2, conversation2, session2, "", "turn.completed", "provider-turn", "", "", histClock.Add(time.Minute))
	var byWindow []step
	for range 3 {
		deleted, caughtUp := pruneStep(t, s2, settledBefore, 2, 100)
		byWindow = append(byWindow, step{deleted, caughtUp})
	}
	if want := []step{{2, false}, {2, false}, {1, true}}; !slices.Equal(byWindow, want) {
		t.Fatalf("steps with a 2-id window = %v, want %v", byWindow, want)
	}
}

// planRecordingDB explains every statement before running it, so the assertion
// is on the statements sqlc compiled in rather than on a copy of their text.
type planRecordingDB struct {
	gen.DBTX
	t     *testing.T
	plans []string
}

func (r *planRecordingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.explain(ctx, query, args)
	return r.DBTX.QueryContext(ctx, query, args...)
}

func (r *planRecordingDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.explain(ctx, query, args)
	return r.DBTX.ExecContext(ctx, query, args...)
}

func (r *planRecordingDB) explain(ctx context.Context, query string, args []any) {
	r.t.Helper()
	rows, err := r.DBTX.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		r.t.Fatalf("explain query plan: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			r.t.Fatalf("scan query plan row: %v", err)
		}
		r.plans = append(r.plans, detail)
	}
	if err := rows.Err(); err != nil {
		r.t.Fatalf("read query plan: %v", err)
	}
}

// The reader seeks a bounded rowid range, and the writer deletes by rowid with
// the turn check on the turns index: neither reads the rest of the archive.
func TestPruneSettledProviderDeltasSeeksARowidRange(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	_ = sqlitetest.MustOpenAt(t, dataDir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	recorder := &planRecordingDB{DBTX: raw, t: t}
	q := gen.New(recorder)
	if _, err := q.SelectProviderEventRetentionWindow(ctx, gen.SelectProviderEventRetentionWindowParams{
		AfterID: 0, ThroughID: retentionWindow, RowLimit: 100,
	}); err != nil {
		t.Fatalf("window: %v", err)
	}
	window := strings.Join(recorder.plans, "\n")
	recorder.plans = nil
	if _, err := q.DeleteSettledProviderMessageDeltas(ctx, gen.DeleteSettledProviderMessageDeltasParams{
		SettledBefore: histClock, Ids: []int64{1, 2},
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	del := strings.Join(recorder.plans, "\n")
	if !strings.Contains(window, "SEARCH conversation_provider_events USING INTEGER PRIMARY KEY (rowid>? AND rowid<?)") {
		t.Errorf("window does not seek a rowid range:\n%s", window)
	}
	if !strings.Contains(del, "USING INTEGER PRIMARY KEY (rowid=?)") ||
		!strings.Contains(del, "USING INDEX idx_conversation_turns_conversation (conversation_id=? AND requested_at<?)") {
		t.Errorf("delete does not go by rowid with the turn check on its index:\n%s", del)
	}
	if strings.Contains(window+del, "SCAN conversation_provider_events") {
		t.Errorf("prune scans the archive:\n%s\n%s", window, del)
	}
}
