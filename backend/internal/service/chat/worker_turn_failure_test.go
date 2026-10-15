package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	turnfailuresvc "github.com/aoagents/agent-orchestrator/backend/internal/service/turnfailure"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestFailedPrimaryWorkerTurnEnqueuesOnceAndHoldsQueue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := testSession
	workerRecord, found, err := h.st.GetSession(ctx, worker)
	if err != nil || !found {
		t.Fatalf("worker session: %v, %v", found, err)
	}
	workerRecord.Kind = domain.KindWorker
	if err := h.st.UpdateSession(ctx, workerRecord); err != nil {
		t.Fatal(err)
	}
	ctrl := h.ctrl
	if _, err := h.svc.Send(ctx, worker, ports.ChatUserMessage{Text: "first", ClientMessageID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	awaitWorkerSnapshot(t, h, ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, worker, ports.ChatUserMessage{Text: "queued", ClientMessageID: "worker-2"}); err != nil {
		t.Fatal(err)
	}
	// ACP wraps the HTTP 504 inside a JSON-RPC -32603 error. The durable
	// message must remain data, while delivery renders only a safe summary.
	failed := ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderEventID: "failure-event",
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateFailed,
		Err: &acpsdk.RequestError{Code: -32603,
			Message: "internal error: upstream returned 504 Gateway Timeout",
			Data:    map[string]any{"requestId": "abc", "provider": "upstream"}},
	}
	h.conv.emit(failed)
	snapshot := awaitWorkerSnapshot(t, h, ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[0].State == domain.TurnStateFailed
	})
	if snapshot.Turns[1].State != domain.TurnStateQueued {
		t.Fatalf("queued turn = %s", snapshot.Turns[1].State)
	}
	deadline := time.Now().Add(3 * time.Second)
	var rows []domain.WorkerTurnFailure
	for time.Now().Before(deadline) {
		rows, err = h.st.ListDueWorkerTurnFailures(ctx, time.Now(), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(rows) != 1 || rows[0].TurnID != snapshot.Turns[0].ID {
		t.Fatalf("outbox = %+v", rows)
	}
	if !strings.Contains(rows[0].ErrorMessage, "-32603") {
		t.Fatalf("lost error: %q", rows[0].ErrorMessage)
	}
	if !hasActivitySignal(h.activity.snapshot(), domain.ActivityWaitingInput, "chat.turn.failed") {
		t.Fatal("failed worker turn did not become waiting_input")
	}
	h.conv.emit(failed)
	time.Sleep(50 * time.Millisecond)
	rows, err = h.st.ListDueWorkerTurnFailures(ctx, time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("replayed failure yielded %d deliveries", len(rows))
	}
	failureSignals := 0
	for _, signal := range h.activity.snapshot() {
		if signal.State == domain.ActivityWaitingInput && signal.Event == "chat.turn.failed" {
			failureSignals++
		}
	}
	if failureSignals != 1 {
		t.Fatalf("failure activity signals = %d, want one", failureSignals)
	}
	orchestrator, err := h.st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := &recordingFailureDelivery{}
	delivery.err = errors.New("semantic acceptance reply lost")
	if err := turnfailuresvc.New(h.st, delivery, nil).RunDue(ctx); err == nil || delivery.calls != 1 {
		t.Fatalf("expected uncertain semantic acknowledgement after one send: err=%v delivery=%+v", err, delivery)
	}
	replacement, err := h.st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		CreatedAt: time.Now().UTC().Add(time.Hour), UpdatedAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == orchestrator.ID {
		t.Fatal("replacement reused original session ID")
	}
	if err := h.st.RetryWorkerTurnFailure(ctx, rows[0].TurnID, time.Now().UTC().Add(-time.Second), "retry after lost acknowledgement"); err != nil {
		t.Fatal(err)
	}
	retryRows, err := h.st.ListDueWorkerTurnFailures(ctx, time.Now().UTC(), 10)
	if err != nil || len(retryRows) != 1 {
		t.Fatalf("due after forced retry = %+v, %v", retryRows, err)
	}
	delivery.err = nil
	if err := turnfailuresvc.New(h.st, delivery, nil).RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if delivery.calls != 2 || delivery.target != orchestrator.ID || delivery.key != "worker-turn-failed:"+rows[0].TurnID {
		t.Fatalf("semantic delivery after restart = %+v", delivery)
	}
	rows, err = h.st.ListDueWorkerTurnFailures(ctx, time.Now(), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("acknowledged outbox = %+v, %v", rows, err)
	}
}

type recordingFailureDelivery struct {
	calls  int
	target domain.SessionID
	key    string
	err    error
}

func (d *recordingFailureDelivery) SendSemantic(_ context.Context, target domain.SessionID, _, key string) error {
	d.calls++
	d.target = target
	d.key = key
	return d.err
}

func TestNonfailedAndAuxiliaryWorkerTurnsDoNotEnqueueFailure(t *testing.T) {
	for _, state := range []domain.TurnState{domain.TurnStateCompleted, domain.TurnStateInterrupted} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			rec, found, err := h.st.GetSession(ctx, testSession)
			if err != nil || !found {
				t.Fatalf("session = %v, %v", found, err)
			}
			rec.Kind = domain.KindWorker
			if err := h.st.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "work", ClientMessageID: "work-1"}); err != nil {
				t.Fatal(err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
			awaitWorkerSnapshot(t, h, h.ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
			})
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: state})
			awaitWorkerSnapshot(t, h, h.ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == state
			})
			rows, err := h.st.ListDueWorkerTurnFailures(ctx, time.Now(), 10)
			if err != nil || len(rows) != 0 {
				t.Fatalf("nonfailed outbox = %+v, %v", rows, err)
			}
		})
	}

	h := newHarness(t)
	ctx := context.Background()
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	rec.Kind = domain.KindWorker
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "work", ClientMessageID: "work-1"}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	awaitWorkerSnapshot(t, h, h.ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "child-turn"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "child-turn", TurnState: domain.TurnStateFailed})
	awaitWorkerSnapshot(t, h, h.ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[1].State == domain.TurnStateFailed
	})
	rows, err := h.st.ListDueWorkerTurnFailures(ctx, time.Now(), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("auxiliary outbox = %+v, %v", rows, err)
	}
}

func TestFailedReviewTurnDoesNotEnqueueWorkerFailure(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	rec, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	rec.Kind = domain.KindWorker
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.UpsertReview(ctx, domain.Review{
		ID: "review-failure", SessionID: testSession, ProjectID: testProject,
		Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateReviewConversation(ctx, "review-conversation", "review-failure", testProject, testSession, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.AppendReviewUserMessage(ctx, conversation.ID, testSession, "review-failure", "review-generation",
		domain.ConversationMessage{ID: "review-message", Text: "review", Origin: domain.MessageOriginHuman, ClientMessageID: "review-1"}, "review-turn", now)
	if err != nil || !created {
		t.Fatalf("append reviewer turn = %v, %v", created, err)
	}
	if err := st.BindTurnToProvider(ctx, "review-turn", "provider-turn-1", now); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleTurn(ctx, conversation.ID, "provider-turn-1", domain.TurnStateFailed, "provider timeout", now); err != nil {
		t.Fatal(err)
	}
	if err := st.EnqueueWorkerTurnFailure(ctx, conversation.ID, "provider-turn-1", now); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListDueWorkerTurnFailures(ctx, time.Now(), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("review failure outbox = %+v, %v", rows, err)
	}
}

func awaitWorkerSnapshot(t *testing.T, h *harness, conversationID string, pred func(store.ConversationSnapshot) bool) store.ConversationSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := h.st.LoadConversationSnapshot(context.Background(), conversationID)
		if err != nil {
			t.Fatal(err)
		}
		if pred(s) {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker snapshot did not reach expected state")
	return store.ConversationSnapshot{}
}
