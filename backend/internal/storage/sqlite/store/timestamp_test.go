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
