package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sqlite "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type receiptConversation struct {
	*terminatingConversation
	ack     func(string) error
	eventMu sync.Mutex
}

func (c *receiptConversation) Close() error {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	return c.fakeConversation.Close()
}

func (c *receiptConversation) emit(events ...ports.ChatEvent) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.fakeConversation.emit(events...)
}

func (c *receiptConversation) AcknowledgeProviderEvent(_ context.Context, id string) error {
	return c.ack(id)
}

type flakyReceiptStore struct {
	chatsvc.Store
	failures atomic.Int32 // -1 fails permanently; positive counts transient failures.
	attempts atomic.Int32
}

func (s *flakyReceiptStore) ProjectProviderEvent(
	ctx context.Context, conversationID string, session domain.SessionID,
	generation, providerEventID, method, payloadJSON string, now time.Time,
	project func(context.Context) error,
) (bool, error) {
	return s.Store.ProjectProviderEvent(ctx, conversationID, session, generation,
		providerEventID, method, payloadJSON, now, func(txCtx context.Context) error {
			if err := project(txCtx); err != nil {
				return err
			}
			if method == string(ports.ChatEventMessageDelta) {
				s.attempts.Add(1)
			}
			if method == string(ports.ChatEventMessageDelta) && s.failures.Load() != 0 {
				if s.failures.Load() > 0 {
					s.failures.Add(-1)
				}
				return errors.New("injected event transaction rollback")
			}
			return nil
		})
}

func TestPersistentProjectionNeverAcknowledgesPastFailedOutput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures int32
	}{
		{"transient failure", 1},
		{"permanent failure", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acknowledged := make(chan string, 4)
			conv := &receiptConversation{
				terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
				ack:                     func(id string) error { acknowledged <- id; return nil },
			}
			var flaky *flakyReceiptStore
			h := newHarnessWithConversationAndStore(t, conv, func(st *sqlite.Store) chatsvc.Store {
				flaky = &flakyReceiptStore{Store: st}
				flaky.failures.Store(tc.failures)
				return flaky
			})
			turn, err := h.ctrl.Send(context.Background(), ports.ChatUserMessage{Text: "first"})
			if err != nil {
				t.Fatal(err)
			}
			conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID},
				ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "answer", ProviderEventID: "output", Delta: "retained answer"},
				ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderEventID: "completion", TurnState: domain.TurnStateCompleted},
			)
			if tc.failures > 0 {
				for _, want := range []string{"output", "completion"} {
					select {
					case got := <-acknowledged:
						if got != want {
							t.Fatalf("ack %q, want %q", got, want)
						}
					case <-time.After(3 * time.Second):
						t.Fatalf("missing ack %s", want)
					}
				}
				snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
					return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
				})
				if len(snapshot.Messages) != 2 || snapshot.Messages[1].Text != "retained answer" {
					t.Fatalf("acknowledged without exactly-once output: %+v", snapshot.Messages)
				}
				if flaky.attempts.Load() != 2 {
					t.Fatalf("transient projection attempts = %d, want 2", flaky.attempts.Load())
				}
				return
			}
			done := make(chan struct{})
			go func() { h.ctrl.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("failed projection did not detach")
			}
			select {
			case id := <-acknowledged:
				t.Fatalf("acknowledged past failed output: %s", id)
			default:
			}
			if flaky.attempts.Load() != 4 {
				t.Fatalf("permanent projection attempts = %d, want initial plus three retries", flaky.attempts.Load())
			}
			if conv.terminated.Load() {
				t.Fatal("projection failure destroyed replay owner")
			}
			snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
			if err != nil || len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateRunning {
				t.Fatalf("failed projection falsely settled turn: %+v, %v", snapshot.Turns, err)
			}
			if err := h.svc.Stop(context.Background(), testSession); err != nil || h.hostStops.Load() != 1 {
				t.Fatalf("explicit stop after failed attachment did not destroy host: calls=%d err=%v", h.hostStops.Load(), err)
			}
		})
	}
}

func TestPersistentCompletionIsAcknowledgedBeforeQueuedTurnDispatch(t *testing.T) {
	for _, ackFails := range []bool{false, true} {
		name := "acknowledged"
		if ackFails {
			name = "ack failure retains queue"
		}
		t.Run(name, func(t *testing.T) {
			var acknowledged, dispatchedBeforeAck atomic.Bool
			conv := &receiptConversation{
				terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
				ack: func(id string) error {
					if id == "completion" {
						if ackFails {
							return errors.New("connection lost before ack")
						}
						acknowledged.Store(true)
					}
					return nil
				},
			}
			conv.onSend = func(id string) {
				if id == "provider-turn-2" && !acknowledged.Load() {
					dispatchedBeforeAck.Store(true)
				}
			}
			h := newHarnessWithConversation(t, conv)
			ctx := context.Background()
			turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "first"})
			if err != nil {
				t.Fatal(err)
			}
			conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
			})
			queued, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "second"})
			if err != nil || queued.State != domain.TurnStateQueued {
				t.Fatalf("queue: %+v, %v", queued, err)
			}
			conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderEventID: "completion", TurnState: domain.TurnStateCompleted})
			if ackFails {
				done := make(chan struct{})
				go func() { h.ctrl.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("ack failure did not detach")
				}
				if len(conv.sentTexts()) != 1 || conv.terminated.Load() {
					t.Fatal("ack failure dispatched queued work or destroyed provider")
				}
			} else {
				h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
					return turnStateByText(t, s)["second"] == domain.TurnStateRunning
				})
			}
			if dispatchedBeforeAck.Load() {
				t.Fatal("queued turn dispatched before previous completion was acknowledged")
			}
		})
	}
}

type completionReconnectConversation struct {
	*receiptConversation
	suppressCompletion bool
}

func (*completionReconnectConversation) ReconnectedLive() bool { return true }

func (c *completionReconnectConversation) ActivateLiveReconnect(ctx context.Context, _ string, _ func([]ports.ChatEvent) []ports.ChatEvent) error {
	if c.suppressCompletion {
		// ACP may acknowledge an already committed result during admission and
		// suppress its semantic replay; the durable queue must still recover.
		return c.AcknowledgeProviderEvent(ctx, "original-completion")
	}
	return nil
}

func TestPersistentCommittedCompletionReplayResumesQueuedWorkAfterLostACK(t *testing.T) {
	for _, suppressCompletion := range []bool{false, true} {
		name := "completion replayed"
		if suppressCompletion {
			name = "completion acknowledged during activation without replay"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var h *harness
			var original, queued domain.ConversationTurn
			committedBeforeLostACK := make(chan error, 1)
			first := &receiptConversation{
				terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
				ack: func(id string) error {
					if id != "original-completion" {
						return nil
					}
					completed, err := h.st.TurnByID(ctx, original.ID)
					if err == nil && completed.State != domain.TurnStateCompleted {
						err = fmt.Errorf("original is %s before failed ACK", completed.State)
					}
					waiting, queueErr := h.st.TurnByID(ctx, queued.ID)
					if err == nil {
						err = queueErr
					}
					if err == nil && waiting.State != domain.TurnStateQueued {
						err = fmt.Errorf("followup is %s before failed ACK", waiting.State)
					}
					committedBeforeLostACK <- err
					return fmt.Errorf("receipt write lost after database COMMIT: %w", ports.ErrChatRecoveryInconclusive)
				},
			}
			h = newHarnessWithConversation(t, first)
			var err error
			original, err = h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "original prompt", ClientMessageID: "original-client"})
			if err != nil {
				t.Fatal(err)
			}
			first.emit(
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: original.ProviderTurnID, ProviderEventID: "original-start"},
				ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: original.ProviderTurnID, ProviderItemID: "original-answer", ProviderEventID: "original-output", Text: "original answer"},
			)
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
			queued, err = h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "queued followup", ClientMessageID: "queued-client"})
			if err != nil || queued.State != domain.TurnStateQueued {
				t.Fatalf("followup must already be durable and queued: %+v, %v", queued, err)
			}
			completion := ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
				ProviderConversationID: first.ProviderConversationID(), ProviderTurnID: original.ProviderTurnID,
				ProviderEventID: "original-completion", TurnState: domain.TurnStateCompleted}
			first.emit(completion)
			select {
			case err := <-committedBeforeLostACK:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("original completion did not COMMIT before the injected ACK failure")
			}
			stopped := make(chan struct{})
			go func() { h.ctrl.Wait(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("failed ACK did not detach the original controller")
			}
			if first.sendCallCount() != 1 || first.terminated.Load() {
				t.Fatal("failed ACK dispatched the queued followup or destroyed the provider")
			}
			replayAcknowledged := make(chan struct{}, 1)
			second := &completionReconnectConversation{receiptConversation: &receiptConversation{
				terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
				ack: func(id string) error {
					if id == "original-completion" {
						select {
						case replayAcknowledged <- struct{}{}:
						default:
						}
					}
					return nil
				},
			}, suppressCompletion: suppressCompletion}
			second.turnSeq = 1 // The provider survived; its next accepted turn is still #2.
			second.onSend = func(id string) {
				second.emit(
					ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id, ProviderEventID: "followup-start"},
					ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: id, ProviderItemID: "followup-answer", ProviderEventID: "followup-output", Text: "followup answer"},
					ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id, ProviderEventID: "followup-completion", TurnState: domain.TurnStateCompleted},
				)
			}
			var ids atomic.Int64
			replacement := chatsvc.New(chatsvc.Options{
				Store: h.st, Sessions: h.st, Reader: fullSnapshotReader(h.st), Activity: h.activity,
				Drivers: fakeRegistry{driver: fakeDriver{conv: second}}, Log: slog.New(slog.DiscardHandler),
				NewID: func() string { return fmt.Sprintf("ack-reconnect-%d", ids.Add(1)) }, Now: h.now,
			})
			t.Cleanup(func() { replacement.StopAll(ctx) })
			controller, err := replacement.Start(ctx, chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: first.ProviderConversationID(),
			})
			if err != nil {
				t.Fatal(err)
			}
			// No explicit DrainQueued call: a successful replacement controller must
			// finish the acknowledged completion's pending effects itself. A duplicate
			// delivery must also leave the newly dispatched successor's ownership intact.
			if !suppressCompletion {
				second.emit(completion, completion)
			}
			select {
			case <-replayAcknowledged:
			case <-time.After(3 * time.Second):
				t.Fatal("replacement did not acknowledge the deduplicated completion")
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				done, doneErr := h.st.TurnByID(ctx, queued.ID)
				if doneErr == nil && done.State == domain.TurnStateCompleted && controller.State() == ports.ChatControllerReady {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			finished, err := h.st.TurnByID(ctx, queued.ID)
			if err != nil || finished.State != domain.TurnStateCompleted || controller.State() != ports.ChatControllerReady {
				t.Fatalf("committed completion was acknowledged on reconnect, but queued work did not resume: queued=%+v state=%s sends=%v err=%v", finished, controller.State(), second.sentTexts(), err)
			}
			if second.sendCallCount() != 1 || len(second.sentTexts()) != 1 || second.sentTexts()[0] != "queued followup" {
				t.Fatalf("followup did not dispatch exactly once after reconnect: calls=%d sends=%v", second.sendCallCount(), second.sentTexts())
			}
			rows, err := h.st.LoadConversationSnapshot(ctx, controller.ConversationID())
			if err != nil || len(rows.Turns) != 2 || len(rows.Messages) != 4 {
				t.Fatalf("replay lost or duplicated original facts: turns=%+v messages=%+v err=%v", rows.Turns, rows.Messages, err)
			}
			archive, err := h.st.ProviderEventsSince(ctx, controller.ConversationID(), 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			completions := make(map[string]int)
			for _, event := range archive {
				if event.Method != string(ports.ChatEventTurnCompleted) {
					continue
				}
				var payload struct {
					ProviderTurnID string `json:"providerTurnId"`
				}
				if err := json.Unmarshal([]byte(event.PayloadJson), &payload); err != nil {
					t.Fatal(err)
				}
				completions[payload.ProviderTurnID]++
			}
			if completions[original.ProviderTurnID] != 1 || completions[finished.ProviderTurnID] != 1 || len(completions) != 2 {
				t.Fatalf("duplicate replay changed completion facts: %v", completions)
			}

		})
	}
}

type failingPreparedConversation struct {
	*deferredConversation
	failText     string
	reconnected  bool
	prepareFails atomic.Int32
	ack          func(string)
}

func (c *failingPreparedConversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if msg.Text == c.failText {
		c.prepareFails.Add(1)
		return ports.ChatTurnRef{}, errors.New("local deferred preparation failed")
	}
	return c.fakeConversation.SendTurn(ctx, msg)
}

func (*failingPreparedConversation) PreservesProviderOnClose() bool { return true }
func (c *failingPreparedConversation) ReconnectedLive() bool        { return c.reconnected }
func (c *failingPreparedConversation) AcknowledgeProviderEvent(_ context.Context, id string) error {
	if c.ack != nil {
		c.ack(id)
	}
	return nil
}

func TestDeferredPrepareFailureHoldsQueueAfterReconnect(t *testing.T) {
	ctx := context.Background()
	deferred := &deferredConversation{fakeConversation: newFakeConversation()}
	deferred.start = func(id string) error {
		deferred.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id, ProviderEventID: "started-" + id})
		return nil
	}
	first := &failingPreparedConversation{deferredConversation: deferred, failText: "older queued B"}
	h := newHarnessWithConversation(t, first)
	a, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "A"})
	if err != nil {
		t.Fatal(err)
	}
	h.advance(time.Second)
	b, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "older queued B"})
	if err != nil || b.State != domain.TurnStateQueued {
		t.Fatalf("queue B: %+v %v", b, err)
	}
	first.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: a.ProviderTurnID, ProviderEventID: "failed-A", TurnState: domain.TurnStateFailed})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["A"] == domain.TurnStateFailed && h.ctrl.State() == ports.ChatControllerReady
	})
	h.advance(time.Second)
	c, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "manual C"})
	if err != nil {
		t.Fatal(err)
	}
	h.advance(time.Second)
	d, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "D stays queued"})
	if err != nil || d.State != domain.TurnStateQueued {
		t.Fatalf("queue D: %+v %v", d, err)
	}
	first.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: c.ProviderTurnID, ProviderEventID: "completed-C", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["manual C"] == domain.TurnStateCompleted && states["older queued B"] == domain.TurnStateFailed
	})
	latest, found, err := h.st.LatestPrimaryTurn(ctx, h.ctrl.ConversationID(), first.ProviderConversationID(), h.ctrl.Generation())
	if err != nil || !found || first.prepareFails.Load() != 1 {
		t.Fatalf("failed preparation facts: latest=%+v found=%v attempts=%d err=%v", latest, found, first.prepareFails.Load(), err)
	}
	h.svc.StopAll(ctx)
	barrier := make(chan struct{}, 1)
	resumedDeferred := &deferredConversation{fakeConversation: newFakeConversation()}
	resumedDeferred.turnSeq = 2 // Only A and C reached the provider's prepared-turn boundary.
	resumedDeferred.start = func(id string) error {
		resumedDeferred.emit(
			ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id, ProviderEventID: "resumed-start"},
			ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id, ProviderEventID: "resumed-completion", TurnState: domain.TurnStateCompleted},
		)
		return nil
	}
	second := &failingPreparedConversation{deferredConversation: resumedDeferred, reconnected: true, ack: func(id string) {
		if id == "startup-barrier" {
			barrier <- struct{}{}
		}
	}}
	var ids atomic.Int64
	replacement := chatsvc.New(chatsvc.Options{
		Store: h.st, Sessions: h.st, Reader: fullSnapshotReader(h.st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: second}}, Log: slog.New(slog.DiscardHandler),
		NewID: func() string { return fmt.Sprintf("prepare-reconnect-%d", ids.Add(1)) }, Now: h.now,
	})
	t.Cleanup(func() { replacement.StopAll(ctx) })
	controller, err := replacement.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: first.ProviderConversationID()})
	if err != nil {
		t.Fatal(err)
	}
	// The projector processes this receipt only after startup recovery finishes.
	second.emit(ports.ChatEvent{Kind: ports.ChatEventDeliveryReceipt, ProviderEventID: "startup-barrier"})
	select {
	case <-barrier:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement did not finish startup recovery")
	}
	queued, err := h.st.TurnByID(ctx, d.ID)
	if err != nil || latest.ID != b.ID || latest.State != domain.TurnStateFailed || latest.ProviderTurnID != "" || queued.State != domain.TurnStateQueued || second.sendCallCount() != 0 || controller.State() != ports.ChatControllerReady {
		t.Fatalf("deferred prepare failure lost dispatch order and released D: latest=%+v D=%+v sends=%v state=%s err=%v", latest, queued, second.sentTexts(), controller.State(), err)
	}
}

func TestProviderPreservationReportRequiresLiveController(t *testing.T) {
	provider := &terminatingConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	if !h.svc.PreservesProviderOnRestart(testSession) || h.svc.PreservesProviderOnRestart("missing") {
		t.Fatal("preservation report does not match established ownership")
	}
	h.svc.StopAll(context.Background())
	if h.hostStops.Load() != 0 {
		t.Fatal("daemon shutdown destroyed persistent ownership")
	}
	if h.svc.PreservesProviderOnRestart(testSession) {
		t.Fatal("missing controller advertised live ownership")
	}
	if err := h.svc.Stop(context.Background(), testSession); err != nil || h.hostStops.Load() != 1 {
		t.Fatalf("explicit stop with no registered controller: calls=%d err=%v", h.hostStops.Load(), err)
	}
	ordinary := newHarness(t)
	if ordinary.svc.PreservesProviderOnRestart(testSession) {
		t.Fatal("ordinary conversation advertised persistent ownership")
	}
}

func TestPersistentTransportFailureRetainsWorkForReconnect(t *testing.T) {
	provider := &terminatingConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "running"})
	if err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped,
		Err: ports.ErrChatRecoveryInconclusive,
	})
	_ = provider.Close()
	h.ctrl.Wait()
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	states := turnStateByText(t, snapshot)
	if states["running"] != domain.TurnStateRunning || states["queued"] != domain.TurnStateQueued {
		t.Fatalf("transport failure settled surviving provider work: %v", states)
	}
	if h.ctrl.State() != ports.ChatControllerStopped || provider.terminated.Load() {
		t.Fatal("lost controller remained live or terminated its persistent provider")
	}
}

func TestPersistentSourceReceiptFollowsAllDerivedProjections(t *testing.T) {
	ctx := context.Background()
	receipts := make(chan string, 4)
	var h *harness
	conv := &receiptConversation{
		terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
		ack: func(id string) error {
			if id != "source-frame:1" && id != "ignored-frame:0" {
				return nil // Only the final derived event can release its source frame.
			}
			snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
			if err != nil {
				return err
			}
			if len(snapshot.Messages) != 2 || snapshot.Messages[1].Text != "one two" {
				return fmt.Errorf("source frame acknowledged before its output committed: %+v", snapshot.Messages)
			}
			receipts <- id
			return nil
		},
	}
	h = newHarnessWithConversation(t, conv)
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID})
	frame := []ports.ChatEvent{
		{Kind: ports.ChatEventMessageDelta, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "answer", ProviderEventID: "source-frame:0", Delta: "one "},
		{Kind: ports.ChatEventMessageDelta, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "answer", ProviderEventID: "source-frame:1", Delta: "two"},
	}
	for range 2 { // The second delivery models a commit followed by loss of the ACK.
		conv.emit(frame...)
		select {
		case id := <-receipts:
			if id != "source-frame:1" {
				t.Fatalf("receipt = %q, want source-frame:1", id)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("source frame was not acknowledged after all derived events committed")
		}
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventDeliveryReceipt, ProviderEventID: "ignored-frame:0", Delta: "must not appear"})
	select {
	case id := <-receipts:
		if id != "ignored-frame:0" {
			t.Fatalf("ignored frame receipt = %q", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("frame without semantic events was not acknowledged")
	}
	archived, err := h.st.ProviderEventsSince(ctx, h.ctrl.ConversationID(), 0, 10)
	if err != nil || len(archived) != 5 || archived[0].Method != "turn.dispatching" {
		t.Fatalf("replayed frame archives = %+v, %v; want dispatch, start, two output events and one receipt", archived, err)
	}
	var record struct {
		ProviderEventID string `json:"providerEventId"`
	}
	if err := json.Unmarshal([]byte(archived[3].PayloadJson), &record); err != nil || record.ProviderEventID != "source-frame:1" {
		t.Fatalf("archived final event = %q, %v", record.ProviderEventID, err)
	}
}

func TestFencedPersistentProjectionNeverAcknowledgesOrPromotesQueue(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		name := "new completion"
		if duplicate {
			name = "previously committed output"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			receipts := make(chan string, 4)
			conv := &receiptConversation{
				terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
				ack:                     func(id string) error { receipts <- id; return nil },
			}
			h := newHarnessWithConversation(t, conv)
			turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "first"})
			if err != nil {
				t.Fatal(err)
			}
			conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
			})
			queued, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "queued"})
			if err != nil || queued.State != domain.TurnStateQueued {
				t.Fatalf("queue: %+v, %v", queued, err)
			}
			event := ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "answer", ProviderEventID: "output", Delta: "saved answer"}
			conv.emit(event)
			select {
			case <-receipts:
			case <-time.After(3 * time.Second):
				t.Fatal("initial output did not commit")
			}
			if err := h.st.ClaimChatControllerGeneration(ctx, testSession, "replacement-generation"); err != nil {
				t.Fatal(err)
			}
			if !duplicate {
				event = ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderEventID: "completion", TurnState: domain.TurnStateCompleted}
			}
			conv.emit(event)
			done := make(chan struct{})
			go func() { h.ctrl.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("fenced controller did not detach")
			}
			select {
			case id := <-receipts:
				t.Fatalf("fenced controller acknowledged %q", id)
			default:
			}
			snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
			if err != nil {
				t.Fatal(err)
			}
			states := turnStateByText(t, snapshot)
			if states["first"] != domain.TurnStateRunning || states["queued"] != domain.TurnStateQueued || len(conv.sentTexts()) != 1 || conv.terminated.Load() {
				t.Fatalf("fenced controller settled work, promoted queue, or destroyed provider: states=%v", states)
			}
		})
	}
}

func TestAwakeReconnectCandidatesRequireObservedTransportLoss(t *testing.T) {
	for _, deliberate := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost attachment", true: "explicit close"}[deliberate], func(t *testing.T) {
			provider := &terminatingConversation{fakeConversation: newFakeConversation()}
			h := newHarnessWithConversation(t, provider)
			if got := h.svc.InterruptedAwakeAgents(); len(got) != 0 {
				t.Fatal("live controller offered as interrupted")
			}
			if deliberate {
				if err := h.ctrl.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				provider.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped, Err: ports.ErrChatRecoveryInconclusive})
				_ = provider.Close()
				h.ctrl.Wait()
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				got := h.svc.InterruptedAwakeAgents()
				if !deliberate && len(got) == 1 {
					if got[0].SessionID != testSession || got[0].Owner.ControllerGeneration != h.ctrl.Generation() || got[0].Owner.ProviderConversationID != h.ctrl.ProviderConversationID() {
						t.Fatalf("candidate lost provider identity: %+v", got)
					}
					return
				}
				if deliberate && len(got) > 0 {
					t.Fatal("explicit close offered for automatic reconnect")
				}
				time.Sleep(time.Millisecond)
			}
			if !deliberate {
				t.Fatal("observed loss did not retain a recovery candidate")
			}
		})
	}
}

func TestAwakeReconnectProofIsInvalidatedByExplicitStop(t *testing.T) {
	provider := &terminatingConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped, Err: ports.ErrChatRecoveryInconclusive})
	_ = provider.Close()
	h.ctrl.Wait()
	var target domain.AwakeAgentTarget
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if list := h.svc.InterruptedAwakeAgents(); len(list) == 1 {
			target = list[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if target.SessionID == "" {
		t.Fatal("missing interrupted proof")
	}
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if len(h.svc.InterruptedAwakeAgents()) != 0 {
		t.Fatal("explicit stop retained recovery proof")
	}
	_, err := h.svc.Start(context.Background(), chatsvc.StartConfig{SessionID: testSession, Harness: domain.HarnessCodex, ReconnectOnly: true, AwakeReconnectTarget: &target, ExpectedControllerOwner: target.Owner, ProviderConversationID: target.Owner.ProviderConversationID})
	if !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("stale proof reached provider startup: %v", err)
	}
}

func TestAwakeReconnectRequiresACKTransportFailureProof(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ackErr        error
		databaseFails bool
		fenced        bool
		explicitClose bool
		wantCandidate bool
	}{
		{name: "observed ACK disconnect", ackErr: fmt.Errorf("write receipt: %w", ports.ErrChatRecoveryInconclusive), wantCandidate: true},
		{name: "ordinary ACK failure", ackErr: context.Canceled},
		{name: "database failure", databaseFails: true},
		{name: "fenced controller", fenced: true},
		{name: "explicit close during ACK disconnect", ackErr: ports.ErrChatRecoveryInconclusive, explicitClose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ackStarted, releaseACK := make(chan struct{}), make(chan struct{})
			provider := &receiptConversation{
				terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
				ack: func(string) error {
					close(ackStarted)
					if tc.explicitClose {
						<-releaseACK
					}
					return tc.ackErr
				},
			}
			provider.closeStarted = make(chan struct{})
			h := newHarnessWithConversationAndStore(t, provider, func(st *sqlite.Store) chatsvc.Store {
				if tc.databaseFails {
					flaky := &flakyReceiptStore{Store: st}
					flaky.failures.Store(-1)
					return flaky
				}
				return st
			})
			if tc.fenced {
				if err := h.st.ClaimChatControllerGeneration(ctx, testSession, "replacement"); err != nil {
					t.Fatal(err)
				}
			}
			provider.emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "answer", ProviderEventID: "output", Delta: "answer"})
			if tc.explicitClose {
				select {
				case <-ackStarted:
				case <-time.After(3 * time.Second):
					t.Fatal("ACK did not start")
				}
				closed := make(chan error, 1)
				go func() { closed <- h.ctrl.Close(ctx) }()
				<-provider.closeStarted
				close(releaseACK)
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := h.svc.Controller(testSession); errors.Is(err, chatsvc.ErrNoController) {
					got := h.svc.InterruptedAwakeAgents()
					if (len(got) == 1) != tc.wantCandidate {
						t.Fatalf("interrupted candidates = %+v, want observed loss=%v", got, tc.wantCandidate)
					}
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("failed projection did not leave the controller registry")
		})
	}
}
