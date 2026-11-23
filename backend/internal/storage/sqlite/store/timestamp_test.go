package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestStoreTimestampPersistenceAndOrdering(t *testing.T) {
	ctx := context.Background()
	s := sqlitetest.MustOpen(t)

	locEST := time.FixedZone("EST", -5*3600)

	// 1. Project Store: Non-UTC RegisteredAt stored as UTC
	projectID := "proj-utc-" + uuid.NewString()[:8]
	tRegisteredEST := time.Date(2026, 4, 1, 14, 0, 0, 0, locEST)
	if err := s.UpsertProject(ctx, domain.ProjectRecord{
		ID:           projectID,
		Path:         "/tmp/" + projectID,
		RegisteredAt: tRegisteredEST,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	pRecord, ok, err := s.GetProject(ctx, projectID)
	if err != nil || !ok {
		t.Fatalf("get project: ok=%v, err=%v", ok, err)
	}
	if pRecord.RegisteredAt.Location() != time.UTC {
		t.Errorf("expected project RegisteredAt location UTC, got %v", pRecord.RegisteredAt.Location())
	}
	if !pRecord.RegisteredAt.Equal(tRegisteredEST) {
		t.Errorf("expected project RegisteredAt equal, got %v != %v", pRecord.RegisteredAt, tRegisteredEST)
	}

	// 2. Session Store: Non-UTC UpdatedAt and Nullable PinnedAt
	sessionID := domain.SessionID("sess-utc-" + uuid.NewString()[:8])
	tCreatedEST := time.Date(2026, 4, 1, 15, 0, 0, 0, locEST)
	createdSession, err := s.CreateSession(ctx, domain.SessionRecord{
		ID:               sessionID,
		ProjectID:        domain.ProjectID(projectID),
		Kind:             domain.KindWorker,
		Harness:          domain.HarnessClaudeCode,
		Activity:         domain.Activity{State: domain.ActivityActive, LastActivityAt: tCreatedEST},
		AutoInjectReview: true,
		CreatedAt:        tCreatedEST,
		UpdatedAt:        tCreatedEST,
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	sRecord, ok, err := s.GetSession(ctx, createdSession.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v, err=%v", ok, err)
	}
	if sRecord.UpdatedAt.Location() != time.UTC {
		t.Errorf("expected session UpdatedAt in UTC, got %v", sRecord.UpdatedAt.Location())
	}
	if sRecord.PinnedAt != nil {
		t.Errorf("expected nil PinnedAt initially, got %v", sRecord.PinnedAt)
	}

	// Pin with non-UTC time
	tPinnedEST := time.Date(2026, 4, 1, 16, 0, 0, 0, locEST)
	if ok, err := s.SetSessionPinned(ctx, createdSession.ID, true, &tPinnedEST, tPinnedEST); err != nil || !ok {
		t.Fatalf("set session pinned: ok=%v, err=%v", ok, err)
	}
	sRecord, ok, err = s.GetSession(ctx, createdSession.ID)
	if err != nil || !ok {
		t.Fatalf("get session pinned: ok=%v, err=%v", ok, err)
	}
	if sRecord.PinnedAt == nil {
		t.Fatal("expected non-nil PinnedAt")
	}
	if sRecord.PinnedAt.Location() != time.UTC {
		t.Errorf("expected PinnedAt in UTC, got %v", sRecord.PinnedAt.Location())
	}
	if !sRecord.PinnedAt.Equal(tPinnedEST) {
		t.Errorf("expected PinnedAt equal to instant")
	}

	// Unpin with nil pointer - verifies null preservation
	if ok, err := s.SetSessionPinned(ctx, createdSession.ID, false, nil, tPinnedEST); err != nil || !ok {
		t.Fatalf("unpin session: ok=%v, err=%v", ok, err)
	}
	sRecord, ok, err = s.GetSession(ctx, createdSession.ID)
	if err != nil || !ok {
		t.Fatalf("get session unpinned: ok=%v, err=%v", ok, err)
	}
	if sRecord.PinnedAt != nil {
		t.Errorf("expected nil PinnedAt after unpin, got %v", sRecord.PinnedAt)
	}

	// 3. Interface Transition Store: Non-UTC transition timestamps
	transitionID := "trans-utc-" + uuid.NewString()[:8]
	tTransEST := time.Date(2026, 4, 1, 17, 0, 0, 0, locEST)
	trans, created, err := s.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID:            transitionID,
		SessionID:     createdSession.ID,
		SourceMode:    domain.SessionModeTUI,
		TargetMode:    domain.SessionModeChat,
		Policy:        domain.SessionInterfaceTransitionDrain,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase:         domain.SessionInterfaceTransitionRequested,
		CreatedAt:     tTransEST,
		UpdatedAt:     tTransEST,
	})
	if err != nil || !created {
		t.Fatalf("create interface transition: %v (created=%v)", err, created)
	}
	if trans.CreatedAt.Location() != time.UTC {
		t.Errorf("expected transition CreatedAt in UTC, got %v", trans.CreatedAt.Location())
	}
	if trans.UpdatedAt.Location() != time.UTC {
		t.Errorf("expected transition UpdatedAt in UTC, got %v", trans.UpdatedAt.Location())
	}
	if !trans.CompletedAt.IsZero() {
		t.Errorf("expected zero CompletedAt for pending transition")
	}

	// Complete transition with non-UTC time
	tCompEST := time.Date(2026, 4, 1, 17, 30, 0, 0, locEST)
	advanced, err := s.AdvanceSessionInterfaceTransition(ctx, transitionID, domain.SessionInterfaceTransitionRequested, domain.SessionInterfaceTransitionCompleted, "", "", "", tCompEST)
	if err != nil || !advanced {
		t.Fatalf("advance transition: %v (advanced=%v)", err, advanced)
	}
	trans, ok, err = s.GetSessionInterfaceTransition(ctx, transitionID)
	if err != nil || !ok {
		t.Fatalf("get transition: %v", err)
	}
	if trans.CompletedAt.IsZero() || trans.CompletedAt.Location() != time.UTC {
		t.Errorf("expected transition CompletedAt in UTC, got %v", trans.CompletedAt)
	}

	// 4. Conversation Store: SQL fence / ordering test with mixed timezones
	// Event A: 12:00 UTC (12:00Z)
	// Event B: 10:00 EST (15:00Z)
	// Chronologically: Event A happens BEFORE Event B (12:00Z < 15:00Z).
	// Under naive textual representation in SQLite:
	// "2026-04-01 10:00:00 -0500" < "2026-04-01 12:00:00 +0000" lexicographically!
	// With UTC normalization:
	// Event A = "2026-04-01 12:00:00Z"
	// Event B = "2026-04-01 15:00:00Z"
	// So Event A sorts before Event B as required.
	convID := "conv-utc-" + uuid.NewString()[:8]
	tBaseUTC := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	conv, err := s.OpenNativeConversation(ctx, convID, domain.ConversationScopeSession, domain.ProjectID(projectID), createdSession.ID, tBaseUTC)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Queue turn 1 at 12:00 UTC
	msg1 := domain.ConversationMessage{
		ID:     "msg-1-" + uuid.NewString()[:8],
		Text:   "first message",
		Origin: domain.MessageOriginHuman,
	}
	turn1ID := "turn-1-" + uuid.NewString()[:8]
	if _, err := s.AppendUserMessage(ctx, conv.ID, createdSession.ID, "gen-1", msg1, turn1ID, tBaseUTC); err != nil {
		t.Fatalf("append user message 1: %v", err)
	}

	// Queue turn 2 at 10:00 EST (which is 15:00 UTC, i.e. 3 hours later)
	tLaterEST := time.Date(2026, 4, 1, 10, 0, 0, 0, locEST)
	msg2 := domain.ConversationMessage{
		ID:     "msg-2-" + uuid.NewString()[:8],
		Text:   "second message",
		Origin: domain.MessageOriginHuman,
	}
	turn2ID := "turn-2-" + uuid.NewString()[:8]
	if _, err := s.AppendUserMessage(ctx, conv.ID, createdSession.ID, "gen-1", msg2, turn2ID, tLaterEST); err != nil {
		t.Fatalf("append user message 2: %v", err)
	}

	// Read queued batch in FIFO order: turn 1 should be first, turn 2 second
	batch, err := s.ClaimQueuedBatch(ctx, conv.ID, false, 10, tLaterEST)
	if err != nil {
		t.Fatalf("claim queued batch: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 turns in batch, got %d", len(batch))
	}
	if batch[0].TurnID != turn1ID {
		t.Errorf("expected turn 1 to be first in FIFO order, got %s", batch[0].TurnID)
	}
	if batch[1].TurnID != turn2ID {
		t.Errorf("expected turn 2 to be second in FIFO order, got %s", batch[1].TurnID)
	}

	// SQL fence test: CancelQueuedTurns with cutoff = 13:00 UTC.
	// Turn 1 (12:00 UTC) must be <= cutoff (cancelled).
	// Turn 2 (15:00 UTC) must be > cutoff (not cancelled).
	// If Turn 2 was stored as non-UTC text ("10:00:00..."), naive text comparison
	// might consider "10:00:00" <= "13:00:00" and cancel Turn 2 incorrectly!
	tCutoffUTC := time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC)
	// First release batch back to queue
	if err := s.ReleaseQueuedBatch(ctx, conv.ID, []string{turn1ID, turn2ID}); err != nil {
		t.Fatalf("release batch: %v", err)
	}

	if err := s.CancelQueuedTurns(ctx, conv.ID, tCutoffUTC, tCutoffUTC); err != nil {
		t.Fatalf("cancel queued turns: %v", err)
	}

	// Verify turn 1 was cancelled (interrupted) and turn 2 remains queued
	t1, err := s.TurnByID(ctx, turn1ID)
	if err != nil {
		t.Fatalf("get turn 1: %v", err)
	}
	if t1.State != domain.TurnStateInterrupted {
		t.Errorf("expected turn 1 interrupted, got %s", t1.State)
	}

	t2, err := s.TurnByID(ctx, turn2ID)
	if err != nil {
		t.Fatalf("get turn 2: %v", err)
	}
	if t2.State != domain.TurnStateQueued {
		t.Errorf("expected turn 2 to remain queued, got %s", t2.State)
	}
}

func TestRollbackTurnsPreservesNonNullMarker(t *testing.T) {
	ctx := context.Background()
	s := sqlitetest.MustOpen(t)

	projectID := "proj-rb-" + uuid.NewString()[:8]
	tBase := time.Now().UTC()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{
		ID:           projectID,
		Path:         "/tmp/" + projectID,
		RegisteredAt: tBase,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	sessionID := domain.SessionID("sess-rb-" + uuid.NewString()[:8])
	createdSession, err := s.CreateSession(ctx, domain.SessionRecord{
		ID:               sessionID,
		ProjectID:        domain.ProjectID(projectID),
		Kind:             domain.KindWorker,
		Harness:          domain.HarnessClaudeCode,
		Activity:         domain.Activity{State: domain.ActivityActive, LastActivityAt: tBase},
		AutoInjectReview: true,
		CreatedAt:        tBase,
		UpdatedAt:        tBase,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	convID := "conv-rb-" + uuid.NewString()[:8]
	conv, err := s.OpenNativeConversation(ctx, convID, domain.ConversationScopeSession, domain.ProjectID(projectID), createdSession.ID, tBase)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	turn1ID := "turn-rb-1-" + uuid.NewString()[:8]
	if _, err := s.AppendUserMessage(ctx, conv.ID, createdSession.ID, "gen-1", domain.ConversationMessage{
		ID:     "msg-rb-1-" + uuid.NewString()[:8],
		Text:   "msg 1",
		Origin: domain.MessageOriginHuman,
	}, turn1ID, tBase); err != nil {
		t.Fatalf("append turn 1: %v", err)
	}

	turn2ID := "turn-rb-2-" + uuid.NewString()[:8]
	if _, err := s.AppendUserMessage(ctx, conv.ID, createdSession.ID, "gen-1", domain.ConversationMessage{
		ID:     "msg-rb-2-" + uuid.NewString()[:8],
		Text:   "msg 2",
		Origin: domain.MessageOriginHuman,
	}, turn2ID, tBase.Add(time.Second)); err != nil {
		t.Fatalf("append turn 2: %v", err)
	}

	// Settle turn 1 as completed with a provider turn id
	if err := s.BindTurnToProvider(ctx, turn1ID, "prov-1", tBase); err != nil {
		t.Fatalf("bind turn 1: %v", err)
	}
	if err := s.SettleTurn(ctx, conv.ID, "prov-1", domain.TurnStateCompleted, "", tBase.Add(time.Second)); err != nil {
		t.Fatalf("settle turn 1: %v", err)
	}

	// Rollback turns with zero-time: must preserve non-null rolled_back_at marker (Valid: true)
	// so downstream InterruptRolledBackQueuedTurns interrupts turn 2.
	discarded, err := s.RollbackTurns(ctx, conv.ID, turn1ID, time.Time{})
	if err != nil {
		t.Fatalf("rollback turns: %v", err)
	}
	if discarded != 2 {
		t.Errorf("expected 2 turns discarded, got %d", discarded)
	}

	// Verify turn 1 is marked rolled back
	t1, err := s.TurnByID(ctx, turn1ID)
	if err != nil {
		t.Fatalf("get turn 1: %v", err)
	}
	if t1.RolledBackAt == nil {
		t.Fatal("expected non-nil RolledBackAt on turn 1")
	}

	// Verify turn 2 was interrupted by InterruptRolledBackQueuedTurns
	t2, err := s.TurnByID(ctx, turn2ID)
	if err != nil {
		t.Fatalf("get turn 2: %v", err)
	}
	if t2.State != domain.TurnStateInterrupted {
		t.Errorf("expected turn 2 to be interrupted by rollback, got %s", t2.State)
	}
}

func TestReservationMarkerPreservedWithZeroTime(t *testing.T) {
	ctx := context.Background()
	s := sqlitetest.MustOpen(t)

	projectID := "proj-res-" + uuid.NewString()[:8]
	tBase := time.Now().UTC()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{
		ID:           projectID,
		Path:         "/tmp/" + projectID,
		RegisteredAt: tBase,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	createdSession, err := s.CreateSession(ctx, domain.SessionRecord{
		ProjectID:        domain.ProjectID(projectID),
		Kind:             domain.KindWorker,
		Harness:          domain.HarnessClaudeCode,
		Activity:         domain.Activity{State: domain.ActivityActive, LastActivityAt: tBase},
		AutoInjectReview: true,
		CreatedAt:        tBase,
		UpdatedAt:        tBase,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	convID := "conv-res-" + uuid.NewString()[:8]
	conv, err := s.OpenNativeConversation(ctx, convID, domain.ConversationScopeSession, domain.ProjectID(projectID), createdSession.ID, tBase)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	turnID := "turn-res-" + uuid.NewString()[:8]
	if _, err := s.AppendUserMessage(ctx, conv.ID, createdSession.ID, "gen-1", domain.ConversationMessage{
		ID:     "msg-res-" + uuid.NewString()[:8],
		Text:   "reserve test",
		Origin: domain.MessageOriginHuman,
	}, turnID, tBase); err != nil {
		t.Fatalf("append turn: %v", err)
	}

	// Claim queued batch with zero time: must write non-NULL promotion_started_at marker
	batch, err := s.ClaimQueuedBatch(ctx, conv.ID, false, 1, time.Time{})
	if err != nil {
		t.Fatalf("claim queued batch: %v", err)
	}
	if len(batch) != 1 || batch[0].TurnID != turnID {
		t.Fatalf("expected batch with turn %s, got %+v", turnID, batch)
	}

	// Second claim must find no queued turn because turn is reserved
	_, err = s.ClaimQueuedBatch(ctx, conv.ID, false, 1, time.Time{})
	if !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Errorf("expected ErrNoQueuedTurn on second claim, got %v", err)
	}
}

func TestZeroValuedTimestampMarkersPreservePresence(t *testing.T) {
	ctx := context.Background()
	s := sqlitetest.MustOpen(t)

	projectID := "proj-zero-" + uuid.NewString()[:8]
	tBase := time.Now().UTC()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{
		ID:           projectID,
		Path:         "/tmp/" + projectID,
		RegisteredAt: tBase,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	sessionID := domain.SessionID("sess-zero-" + uuid.NewString()[:8])
	createdSession, err := s.CreateSession(ctx, domain.SessionRecord{
		ID:               sessionID,
		ProjectID:        domain.ProjectID(projectID),
		Kind:             domain.KindWorker,
		Harness:          domain.HarnessClaudeCode,
		Activity:         domain.Activity{State: domain.ActivityIdle, LastActivityAt: tBase.Add(-2 * time.Hour)},
		AutoInjectReview: true,
		CreatedAt:        tBase,
		UpdatedAt:        tBase,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 1. Hibernation marker: SetSessionHibernated with &time.Time{} must write non-NULL marker
	// and reload with non-nil pointer.
	zeroTime := time.Time{}
	if applied, err := s.SetSessionHibernated(ctx, createdSession.ID, createdSession.Revision, &zeroTime); err != nil || !applied {
		t.Fatalf("set session hibernated with zero time: applied=%v, err=%v", applied, err)
	}
	reloaded, ok, err := s.GetSession(ctx, createdSession.ID)
	if err != nil || !ok {
		t.Fatalf("get session: %v", err)
	}
	if reloaded.HibernatedAt == nil {
		t.Fatal("expected non-nil HibernatedAt after hibernating with zero timestamp pointer")
	}
	if !reloaded.HibernatedAt.IsZero() {
		t.Errorf("expected zero-valued timestamp in HibernatedAt, got %v", *reloaded.HibernatedAt)
	}
	candidates, err := s.ListChatHibernationCandidates(ctx)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	for _, c := range candidates {
		if c == createdSession.ID {
			t.Errorf("hibernated session %s should not be returned as hibernation candidate", c)
		}
	}

	// 2. Terminal turn response with zero timestamp must close turn so subsequent request creates new turn
	if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
		SessionID: createdSession.ID, Harness: domain.HarnessClaudeCode, NativeSessionID: "term-1",
		UserRequest: "first prompt", RequestedAt: tBase,
	}); err != nil {
		t.Fatalf("record terminal turn request 1: %v", err)
	}
	if err := s.RecordTerminalTurnResponse(ctx, createdSession.ID, "first response", time.Time{}); err != nil {
		t.Fatalf("record terminal turn response: %v", err)
	}
	if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
		SessionID: createdSession.ID, Harness: domain.HarnessClaudeCode, NativeSessionID: "term-1",
		UserRequest: "second prompt", RequestedAt: tBase.Add(time.Minute),
	}); err != nil {
		t.Fatalf("record terminal turn request 2: %v", err)
	}
	if err := s.RecordTerminalTurnResponse(ctx, createdSession.ID, "second response", time.Time{}); err != nil {
		t.Fatalf("record terminal turn response 2: %v", err)
	}
	turns, err := s.ListTerminalTurns(ctx, createdSession.ID)
	if err != nil {
		t.Fatalf("list terminal turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].UserRequest != "first prompt" || turns[0].AgentResponse != "first response" {
		t.Errorf("unexpected turn 0: %+v", turns[0])
	}
	if turns[1].UserRequest != "second prompt" || turns[1].AgentResponse != "second response" {
		t.Errorf("unexpected turn 1: %+v", turns[1])
	}

	// 3. Transition message delivery with zero timestamp must mark delivered_at non-NULL
	// and remove it from ListPendingSessionInterfaceTransitionMessages.
	transitionID := "trans-zero-" + uuid.NewString()[:8]
	_, _, err = s.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID:            transitionID,
		SessionID:     createdSession.ID,
		SourceMode:    domain.SessionModeTUI,
		TargetMode:    domain.SessionModeChat,
		Policy:        domain.SessionInterfaceTransitionDrain,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase:         domain.SessionInterfaceTransitionRequested,
		CreatedAt:     tBase,
		UpdatedAt:     tBase,
	})
	if err != nil {
		t.Fatalf("create transition: %v", err)
	}
	if err := s.EnqueueSessionInterfaceTransitionMessage(ctx, transitionID, "client-msg-zero", "hello context", tBase, ports.MessageDeliveryOptions{}); err != nil {
		t.Fatalf("enqueue transition message: %v", err)
	}
	pending, err := s.ListPendingSessionInterfaceTransitionMessages(ctx, transitionID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("expected 1 pending message, got %d (err=%v)", len(pending), err)
	}
	if err := s.MarkSessionInterfaceTransitionMessageDelivered(ctx, pending[0].ID, time.Time{}); err != nil {
		t.Fatalf("mark message delivered: %v", err)
	}
	pendingAfter, err := s.ListPendingSessionInterfaceTransitionMessages(ctx, transitionID)
	if err != nil {
		t.Fatalf("list pending messages after delivery: %v", err)
	}
	if len(pendingAfter) != 0 {
		t.Errorf("expected 0 pending messages after zero-time delivery, got %d", len(pendingAfter))
	}

	// 4. Conversation turn settlement with zero timestamp must record completed_at non-NULL.
	convID := "conv-zero-" + uuid.NewString()[:8]
	conv, err := s.OpenNativeConversation(ctx, convID, domain.ConversationScopeSession, domain.ProjectID(projectID), createdSession.ID, tBase)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	turnID := "turn-zero-" + uuid.NewString()[:8]
	if _, err := s.AppendUserMessage(ctx, conv.ID, createdSession.ID, "gen-1", domain.ConversationMessage{
		ID:     "msg-zero-1-" + uuid.NewString()[:8],
		Text:   "turn test",
		Origin: domain.MessageOriginHuman,
	}, turnID, tBase); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if err := s.BindTurnToProvider(ctx, turnID, "prov-zero-1", tBase); err != nil {
		t.Fatalf("bind turn: %v", err)
	}
	if err := s.SettleTurn(ctx, conv.ID, "prov-zero-1", domain.TurnStateCompleted, "", time.Time{}); err != nil {
		t.Fatalf("settle turn with zero time: %v", err)
	}
	settledTurn, err := s.TurnByID(ctx, turnID)
	if err != nil {
		t.Fatalf("get turn: %v", err)
	}
	if settledTurn.CompletedAt == nil {
		t.Fatal("expected non-nil CompletedAt on settled turn")
	}
	if !settledTurn.CompletedAt.IsZero() {
		t.Errorf("expected zero-valued timestamp in CompletedAt, got %v", *settledTurn.CompletedAt)
	}
	latestSettled, err := s.LatestVisibleUserTurnSettled(ctx, conv.ID, createdSession.ID)
	if err != nil || !latestSettled {
		t.Errorf("expected LatestVisibleUserTurnSettled to be true, got settled=%v, err=%v", latestSettled, err)
	}

	// 5. Stale harness and switch failure markers with zero timestamp must set non-NULL markers.
	// First wake up the session so it can be matched by MarkHarnessSessionsStale
	reloadedLatest, ok, err := s.GetSession(ctx, createdSession.ID)
	if err != nil || !ok {
		t.Fatalf("get session: %v", err)
	}
	if applied, err := s.SetSessionHibernated(ctx, createdSession.ID, reloadedLatest.Revision, nil); err != nil || !applied {
		t.Fatalf("un-hibernate session: applied=%v, err=%v", applied, err)
	}
	matched, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "v2.0.0", time.Time{})
	if err != nil {
		t.Fatalf("mark harness stale: %v", err)
	}
	if matched == 0 {
		t.Fatalf("expected at least 1 session marked stale")
	}
	if err := s.SetSessionVersionSwitchFailed(ctx, createdSession.ID, "v2.0.0", time.Time{}); err != nil {
		t.Fatalf("set session version switch failed: %v", err)
	}
	reloadedAfterStale, ok, err := s.GetSession(ctx, createdSession.ID)
	if err != nil || !ok {
		t.Fatalf("get session after stale mark: %v", err)
	}
	if reloadedAfterStale.StaleBinary == nil {
		t.Fatal("expected non-nil StaleBinary after zero-time stale mark")
	}
	if !reloadedAfterStale.StaleBinary.Since.IsZero() {
		t.Errorf("expected zero-valued Since, got %v", reloadedAfterStale.StaleBinary.Since)
	}
	if reloadedAfterStale.StaleBinary.SwitchFailedAt == nil {
		t.Fatal("expected non-nil SwitchFailedAt after failure mark")
	}
	if !reloadedAfterStale.StaleBinary.SwitchFailedAt.IsZero() {
		t.Errorf("expected zero-valued SwitchFailedAt, got %v", *reloadedAfterStale.StaleBinary.SwitchFailedAt)
	}
}
