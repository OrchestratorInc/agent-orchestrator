package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func failUsageLimitedTurn(t *testing.T, h *harness, message string) domain.ConversationTurn {
	t.Helper()
	return failUsageLimitedTurnAt(t, h, message, time.Time{})
}

func failUsageLimitedTurnAt(t *testing.T, h *harness, message string, reset time.Time) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "finish this task", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(message), UsageLimitResetsAt: reset})
	snapshot := failedTurnSnapshot(t, h, turn.ID)
	failed, _ := turnByID(snapshot, turn.ID)
	return failed
}

const timedUsageLimit = "You've hit your session limit · resets 3:31pm (Asia/Calcutta)"

var errUsageLimitTemporaryRead = errors.New("database is temporarily unavailable")

type usageLimitReadStore struct {
	*sqlite.Store
	reads         atomic.Int32
	failTurn      atomic.Bool
	failPrompt    atomic.Bool
	failSession   atomic.Bool
	attemptBudget atomic.Int64
	afterLatest   func()
}

func (s *usageLimitReadStore) TurnByID(ctx context.Context, id string) (domain.ConversationTurn, error) {
	s.reads.Add(1)
	if s.failTurn.Swap(false) {
		return domain.ConversationTurn{}, errUsageLimitTemporaryRead
	}
	return s.Store.TurnByID(ctx, id)
}

func (s *usageLimitReadStore) RetryPrompt(ctx context.Context, conversationID, id string) (domain.RetryPrompt, error) {
	s.reads.Add(1)
	if s.failPrompt.Swap(false) {
		return domain.RetryPrompt{}, errUsageLimitTemporaryRead
	}
	return s.Store.RetryPrompt(ctx, conversationID, id)
}

func (s *usageLimitReadStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.reads.Add(1)
	if s.failSession.Swap(false) {
		return domain.SessionRecord{}, false, errUsageLimitTemporaryRead
	}
	if deadline, ok := ctx.Deadline(); ok {
		s.attemptBudget.Store(int64(time.Until(deadline)))
	}
	return s.Store.GetSession(ctx, id)
}

func (s *usageLimitReadStore) LatestAutoResumeTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error) {
	s.reads.Add(1)
	turn, err := s.Store.LatestAutoResumeTurn(ctx, conversationID)
	if err == nil && s.afterLatest != nil {
		hook := s.afterLatest
		s.afterLatest = nil
		hook()
	}
	return turn, err
}

func newUsageLimitReadHarness(t *testing.T) (*harness, *usageLimitReadStore) {
	t.Helper()
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	wrapped := &usageLimitReadStore{Store: h.st}
	provider := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: wrapped, Reader: fullSnapshotReader(h.st), PageReader: boundedSnapshotReader(h.st), Sessions: wrapped,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	h.svc, h.ctrl, h.conv = svc, controller, provider
	return h, wrapped
}

func TestUsageLimitFutureResetSkipsEligibilityReads(t *testing.T) {
	h, wrapped := newUsageLimitReadHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	reads := wrapped.reads.Load()
	for range 5 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := wrapped.reads.Load(); got != reads {
		t.Fatalf("future sweep did %d eligibility reads", got-reads)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if wrapped.reads.Load() == reads || len(h.conv.sentMessages()) != 2 {
		t.Fatal("deadline did not trigger validation and one retry")
	}
	if budget := time.Duration(wrapped.attemptBudget.Load()); budget < time.Minute || budget > 90*time.Second {
		t.Fatalf("automatic attempt budget = %s, want room for a 60-second native wake", budget)
	}
}

func TestUsageLimitTransientReadsBackOffAndRecover(t *testing.T) {
	for _, stage := range []string{"turn", "prompt", "session"} {
		t.Run(stage, func(t *testing.T) {
			h, wrapped := newUsageLimitReadHarness(t)
			ctx := context.Background()
			source := failUsageLimitedTurn(t, h, timedUsageLimit)
			if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
				t.Fatal(err)
			}
			h.advance(time.Minute)
			switch stage {
			case "turn":
				wrapped.failTurn.Store(true)
			case "prompt":
				wrapped.failPrompt.Store(true)
			case "session":
				wrapped.failSession.Store(true)
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, errUsageLimitTemporaryRead) {
				t.Fatalf("transient sweep = %v", err)
			}
			after, err := h.st.TurnByID(ctx, source.ID)
			if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
				t.Fatalf("temporary read failure consumed intent: %+v, %v", after.UsageLimit, err)
			}
			reads := wrapped.reads.Load()
			h.advance(29 * time.Second)
			for range 3 {
				if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if wrapped.reads.Load() != reads || len(h.conv.sentMessages()) != 1 {
				t.Fatal("temporary failure was retried during backoff")
			}
			h.advance(time.Second)
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
				t.Fatal(err)
			}
			after, err = h.st.TurnByID(ctx, source.ID)
			if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" || len(h.conv.sentMessages()) != 2 {
				t.Fatalf("recovered sweep did not dispatch once: %+v, %v", after.UsageLimit, err)
			}
		})
	}
}

func TestUsageLimitManualRetryDuringSweepKeepsConsumedSourceClean(t *testing.T) {
	h, wrapped := newUsageLimitReadHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	wrapped.afterLatest = func() {
		if _, err := h.svc.RetryTurn(ctx, testSession, source.ID); err != nil {
			t.Fatalf("manual retry: %v", err)
		}
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" || len(h.conv.sentMessages()) != 2 {
		t.Fatalf("sweep overwrote manually consumed intent: %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeRecoversAfterControllerHandoff(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.BeginIdleBranchHandoff(ctx); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatalf("handoff sweep = %v", err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("handoff consumed intent: %+v, %v", after.UsageLimit, err)
	}
	h.ctrl.AbortHandoff()
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("handoff recovery ignored backoff")
	}
	h.advance(30 * time.Second)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("handoff recovery did not resume the armed prompt")
	}
}

type cancelledUsageLimitScanStore struct {
	*sqlite.Store
	afterScan func()
}

func (s *cancelledUsageLimitScanStore) AutoResumeTurns(ctx context.Context) ([]domain.ConversationTurn, error) {
	turns, err := s.Store.AutoResumeTurns(ctx)
	if err == nil && s.afterScan != nil {
		s.afterScan()
	}
	return turns, err
}

func TestUsageLimitCancellationAfterSweepScanPreventsDispatch(t *testing.T) {
	var wrapped *cancelledUsageLimitScanStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
		wrapped = &cancelledUsageLimitScanStore{Store: st}
		return wrapped
	})
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	wrapped.afterScan = func() {
		if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("sweep used a stale consent snapshot after cancellation")
	}
}

func TestUsageLimitAutoResumeWaitsAndDispatchesOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if source.UsageLimit == nil || source.UsageLimit.ResetsAt == nil || !source.UsageLimit.ResetsAt.Equal(h.now().Add(time.Minute)) {
		t.Fatalf("historical limit = %+v, want an absolute reset in one minute", source.UsageLimit)
	}
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("auto-resume sent before reset")
	}
	h.advance(time.Minute)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatalf("provider received %d messages, want one retry", len(h.conv.sentMessages()))
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit == nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source = %+v, %v, want consumed consent", after, err)
	}
}

func TestUsageLimitAutoResumeCancellationWorksWhileBusy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "new work", ClientMessageID: "new", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, false); err != nil {
		t.Fatalf("cancel while busy: %v", err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("cancelled source was dispatched")
	}
}

func TestUsageLimitAutoResumeStopsAfterNewerCompletedTurn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	newer, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "continue differently", ClientMessageID: "new", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: newer.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		turn, found := turnByID(s, newer.ID)
		return found && turn.State == domain.TurnStateCompleted
	})
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); !errors.Is(err, chatsvc.ErrUsageLimitAutoResumeInvalid) {
		t.Fatalf("enable superseded source = %v", err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || !strings.Contains(after.UsageLimit.ResumeError, "newer turn") {
		t.Fatalf("source limit = %+v, %v", after.UsageLimit, err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("superseded source was retried")
	}
}

func TestUsageLimitAutoResumeDurableAcrossServiceRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	project, _, err := h.st.GetProject(ctx, string(testProject))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	provider := newFakeConversation()
	provider.turnSeq = 1 // Native resume preserves the provider's existing turn IDs.
	restarted := chatsvc.New(chatsvc.Options{
		Store: reopened, Reader: fullSnapshotReader(reopened), Sessions: reopened,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Stop(ctx, testSession) })
	if err := restarted.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(provider.sentMessages()) != 0 {
		t.Fatal("restart advanced the reset deadline")
	}
	h.advance(time.Minute)
	if err := restarted.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(provider.sentMessages()) != 1 || provider.sentMessages()[0].Text != "finish this task" {
		t.Fatalf("resumed sends = %+v", provider.sentMessages())
	}
}

func TestUsageLimitAutoResumeWakesHibernatedChatWithoutAView(t *testing.T) {
	h, old := settledHibernationHarness(t, "")
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
		t.Fatalf("hibernate = %v, %v", slept, err)
	}
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	wakeService := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumed}}, NewID: uuid.NewString, Now: h.now,
	})
	t.Cleanup(func() { _ = wakeService.Stop(ctx, testSession) })
	wakeCalls := 0
	wakeService.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		wakeCalls++
		record, _, err := h.st.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if applied, err := h.st.SetSessionHibernated(ctx, id, record.Revision, nil); err != nil || !applied {
			return errors.New("could not clear hibernation marker")
		}
		_, err = wakeService.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject,
			Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID()})
		return err
	})
	h.advance(time.Minute)
	if err := wakeService.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if wakeCalls != 1 || len(resumed.sentMessages()) != 1 {
		t.Fatalf("wake calls = %d, sends = %+v", wakeCalls, resumed.sentMessages())
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source = %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitDaemonShutdownKeepsUnconsumedIntent(t *testing.T) {
	h := newHarness(t)
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	ctx := context.Background()
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := h.svc.ResumeUsageLimitedTurns(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown sweep = %v", err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume {
		t.Fatalf("shutdown lost consent: %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeStopsAfterRollback(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.RollbackTurns(ctx, source.ConversationID, source.ID, h.now()); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError == "" || len(h.conv.sentMessages()) != 1 {
		t.Fatalf("rolled-back source = %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeDefersBusyCompaction(t *testing.T) {
	provider := newCompactingConversation()
	h := newHarnessWithConversation(t, provider)
	h.conv = provider.fakeConversation
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Compact(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, chatsvc.ErrTurnRunning) {
		t.Fatalf("busy sweep = %v", err)
	}
	if len(provider.sentMessages()) != 1 {
		t.Fatal("auto-resume dispatched during compaction")
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("busy source = %+v, %v", after.UsageLimit, err)
	}
}

type waitingUsageLimitCompaction struct {
	*compactingConversation
	started chan struct{}
	release chan struct{}
}

func (c *waitingUsageLimitCompaction) Compact(context.Context) (ports.ChatCompactionResult, error) {
	close(c.started)
	<-c.release
	return ports.ChatCompactionResult{}, nil
}

func TestUsageLimitAutoResumeDoesNotWaitForStuckDispatchLock(t *testing.T) {
	provider := &waitingUsageLimitCompaction{compactingConversation: newCompactingConversation(), started: make(chan struct{}), release: make(chan struct{})}
	h := newHarnessWithConversation(t, provider)
	h.conv = provider.fakeConversation
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	compacted := make(chan struct{})
	go func() { _, _ = h.svc.Compact(ctx, testSession); close(compacted) }()
	<-provider.started
	defer func() { close(provider.release); <-compacted }()
	h.advance(time.Minute)
	swept := make(chan error, 1)
	go func() { swept <- h.svc.ResumeUsageLimitedTurns(ctx) }()
	select {
	case err := <-swept:
		if !errors.Is(err, chatsvc.ErrTurnRunning) {
			t.Fatalf("locked sweep = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("automatic retry blocked behind provider I/O")
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" || len(provider.sentMessages()) != 1 {
		t.Fatalf("stuck provider source = %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeStopsWhenSessionTerminated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	record, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	record.IsTerminated = true
	if err := h.st.UpdateSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError == "" {
		t.Fatalf("terminated source = %+v, %v", after.UsageLimit, err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("auto-resume sent to terminated session")
	}
}

func TestUsageLimitAutoResumeUnknownResetRefusesConsent(t *testing.T) {
	h := newHarness(t)
	source := failUsageLimitedTurn(t, h, "Usage limit reached. Resets tomorrow.")
	if err := h.svc.SetTurnAutoResume(context.Background(), testSession, source.ID, true); !errors.Is(err, chatsvc.ErrUsageLimitResetUnknown) {
		t.Fatalf("enable = %v, want unknown reset", err)
	}
}

func TestUsageLimitDispatchFailureConsumesSourceWithoutRepeating(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.conv.mu.Lock()
	h.conv.sendErr = errors.New("provider disconnected")
	h.conv.mu.Unlock()
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err == nil || !strings.Contains(err.Error(), "provider disconnected") {
		t.Fatalf("dispatch failure = %v", err)
	}
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source limit = %+v, %v", after.UsageLimit, err)
	}
	retryID, found, err := h.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID)
	if err != nil || !found {
		t.Fatalf("failed attempt = %q, %v, %v", retryID, found, err)
	}
	retry, err := h.st.TurnByID(ctx, retryID)
	if err != nil || retry.State != domain.TurnStateFailed || !strings.Contains(retry.ErrorMessage, "provider disconnected") {
		t.Fatalf("failed attempt lost its outcome: %+v, %v", retry, err)
	}
	h.conv.mu.Lock()
	calls := h.conv.sendCalls
	h.conv.mu.Unlock()
	if calls != 2 {
		t.Fatalf("provider sends = %d, want original plus one failed attempt", calls)
	}
}

func TestUsageLimitManualAndScheduledRetryCannotDuplicate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = h.svc.RetryTurn(ctx, testSession, source.ID) }()
	go func() { defer wg.Done(); _ = h.svc.ResumeUsageLimitedTurns(ctx) }()
	wg.Wait()
	if len(h.conv.sentMessages()) != 2 {
		t.Fatalf("provider sends = %d, want original plus one retry", len(h.conv.sentMessages()))
	}
}

func failUsageLimitedEvent(t *testing.T, h *harness, text, message string) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: text, ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(message)})
	failed, _ := turnByID(failedTurnSnapshot(t, h, turn.ID), turn.ID)
	return failed
}

func assertContinuedEvent(t *testing.T, sent ports.ChatUserMessage, event string) {
	t.Helper()
	if !strings.HasPrefix(sent.Text, "Continue from where you stopped.") || !sent.Continuation || sent.Origin != domain.MessageOriginAutomation {
		t.Fatalf("sent = %+v, want a continue message", sent)
	}
	if !strings.Contains(sent.Text, `{"originalRequest":"`+event+`"}`) {
		t.Fatalf("sent = %q, want it to quote %q", sent.Text, event)
	}
}

func TestUsageLimitAutoResumeContinuesAnEventTurn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sent := h.conv.sentMessages()
	if len(sent) != 2 {
		t.Fatalf("provider received %d messages, want the event and one continue", len(sent))
	}
	assertContinuedEvent(t, sent[1], "CI is failing on PR 12")
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source = %+v, %v, want consumed consent", after.UsageLimit, err)
	}
}

func TestUsageLimitContinueRefusedAgainResumesAgain(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	first, err := h.svc.RetryTurn(ctx, testSession, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: first.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, first.ID)
	if _, err := h.svc.RetryTurn(ctx, testSession, first.ID); err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentMessages()
	if len(sent) != 3 {
		t.Fatalf("provider received %d messages, want three", len(sent))
	}
	assertContinuedEvent(t, sent[1], "CI is failing on PR 12")
	assertContinuedEvent(t, sent[2], "CI is failing on PR 12")
}

func TestRetryContinuesFromTheLatestUsageLimitedEvent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if _, err := h.svc.RetryTurn(ctx, testSession, older.ID); !errors.Is(err, chatsvc.ErrTurnNotRetryable) {
		t.Fatalf("retry an older event = %v, want ErrTurnNotRetryable", err)
	}
	if _, err := h.svc.RetryTurn(ctx, testSession, newer.ID); err != nil {
		t.Fatal(err)
	}
	assertContinuedEvent(t, h.conv.sentMessages()[2], `CI is failing on PR 12\n\nCI is failing on PR 13`)
}

func TestRetryRefusesAnEventTurnThatFailedForAnotherReason(t *testing.T) {
	h := newHarness(t)
	source := failUsageLimitedEvent(t, h, "CI is failing on PR 12", "connection lost")
	if _, err := h.svc.RetryTurn(context.Background(), testSession, source.ID); !errors.Is(err, chatsvc.ErrTurnNotRetryable) {
		t.Fatalf("retry = %v, want ErrTurnNotRetryable", err)
	}
}

func TestUsageLimitContinueQuotesEveryBatchedMessage(t *testing.T) {
	for name, authoredByUser := range map[string]bool{"events": false, "led by a message the user relayed": true} {
		t.Run(name, func(t *testing.T) { testContinueQuotesBatch(t, authoredByUser) })
	}
}

func testContinueQuotesBatch(t *testing.T, authoredByUser bool) {
	h := newHarness(t)
	ctx := context.Background()
	busy, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "work", ClientMessageID: "busy", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	var lead domain.ConversationTurn
	for i, text := range []string{"CI is failing on PR 12", "CI is failing on PR 13"} {
		queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: text, ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginAutomation,
			AuthoredByUser: authoredByUser && i == 0})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			lead = queued
		}
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: busy.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	running, _ := turnByID(h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		turn, found := turnByID(s, lead.ID)
		return found && turn.State == domain.TurnStateRunning && turn.ProviderTurnID != ""
	}), lead.ID)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: running.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, lead.ID)
	if _, err := h.svc.RetryTurn(ctx, testSession, lead.ID); err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentMessages()
	last := sent[len(sent)-1]
	for _, want := range []string{"Continue from where you stopped.", "CI is failing on PR 12", "CI is failing on PR 13"} {
		if !strings.Contains(last.Text, want) {
			t.Fatalf("sent = %q, want it to contain %q", last.Text, want)
		}
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, lead.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored := snapshot.Messages[len(snapshot.Messages)-1]; stored.Text != "Continue from where you stopped." || !stored.Continuation || stored.AuthoredByUser {
		t.Fatalf("stored = %+v, want only a continue marker", stored)
	}
}

func TestRetryResendsALimitedMessageTheUserRelayed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "why this change?", ClientMessageID: "relayed", Origin: domain.MessageOriginAutomation, AuthoredByUser: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)
	if _, err := h.svc.RetryTurn(ctx, testSession, turn.ID); err != nil {
		t.Fatal(err)
	}
	if sent := h.conv.sentMessages()[1]; sent.Text != "why this change?" || sent.Continuation {
		t.Fatalf("sent = %+v, want the user's own message again", sent)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, turn.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if last := snapshot.Messages[len(snapshot.Messages)-1]; !last.AuthoredByUser {
		t.Fatalf("retried message = %+v, want it still credited to the user", last)
	}
}

func TestUsageLimitAutoResumeWaitsForARunningEvent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, true); err != nil {
		t.Fatal(err)
	}
	newer, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "CI is failing on PR 13", ClientMessageID: "newer", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, chatsvc.ErrTurnRunning) {
		t.Fatal(err)
	}
	if after, err := h.st.TurnByID(ctx, older.ID); err != nil || !after.UsageLimit.AutoResume {
		t.Fatalf("older limit = %+v, %v, want consent kept while the newer event runs", after.UsageLimit, err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: newer.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New("You've hit your weekly limit · resets 5:31pm (Asia/Calcutta)")})
	failed, _ := turnByID(failedTurnSnapshot(t, h, newer.ID), newer.ID)
	if !failed.UsageLimit.AutoResume {
		t.Fatalf("newer limit = %+v, want the armed consent", failed.UsageLimit)
	}
}

func TestUsageLimitAutoResumeMovesFromARefusedContinue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	resumed, err := h.svc.RetryTurn(ctx, testSession, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: resumed.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, resumed.ID)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, resumed.ID, true); err != nil {
		t.Fatal(err)
	}
	if newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit); !newer.UsageLimit.AutoResume {
		t.Fatalf("newer limit = %+v, want the armed consent", newer.UsageLimit)
	}
}

func TestUsageLimitAutoResumeDoesNotMoveFromAnUndoneEvent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.RollbackTurns(ctx, older.ConversationID, older.ID, h.now()); err != nil {
		t.Fatal(err)
	}
	if newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit); newer.UsageLimit.AutoResume {
		t.Fatal("an undone event armed a newer one")
	}
}

func TestUsageLimitAutoResumeMovesToANewerRefusedEvent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, true); err != nil {
		t.Fatal(err)
	}
	newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if newer.UsageLimit == nil || !newer.UsageLimit.AutoResume || newer.UsageLimit.ResetsAt == nil {
		t.Fatalf("newer limit = %+v, want the armed consent", newer.UsageLimit)
	}
	after, err := h.st.TurnByID(ctx, older.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("older limit = %+v, %v, want consent moved without an error", after.UsageLimit, err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentMessages()
	if len(sent) != 3 {
		t.Fatalf("provider received %d messages, want both events and one continue", len(sent))
	}
	assertContinuedEvent(t, sent[2], `CI is failing on PR 12\n\nCI is failing on PR 13`)
}

func TestUsageLimitAutoResumeDoesNotMovePastTheUsersMessage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, true); err != nil {
		t.Fatal(err)
	}
	human, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "carry on", ClientMessageID: "human", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: human.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		turn, found := turnByID(s, human.ID)
		return found && turn.State == domain.TurnStateCompleted
	})
	if newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit); newer.UsageLimit.AutoResume {
		t.Fatal("consent the user's own message superseded armed a newer event")
	}
}

func TestUsageLimitAutoResumeKeepsTheKnownResetForANewerEvent(t *testing.T) {
	h := newHarness(t)
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(context.Background(), testSession, older.ID, true); err != nil {
		t.Fatal(err)
	}
	newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", "You've hit your session limit")
	if !newer.UsageLimit.AutoResume || newer.UsageLimit.ResetsAt == nil || !newer.UsageLimit.ResetsAt.Equal(*older.UsageLimit.ResetsAt) {
		t.Fatalf("newer limit = %+v, want it armed for %v", newer.UsageLimit, older.UsageLimit.ResetsAt)
	}
}

func TestUsageLimitAutoResumeStaysOnTheLatestTurn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	running, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "CI is failing on PR 12", ClientMessageID: "running", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	held, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "CI is failing on PR 13", ClientMessageID: "held", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: running.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, running.ID)
	resumed, err := h.svc.RetryTurn(ctx, testSession, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: resumed.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, resumed.ID)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, resumed.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "CI is failing on PR 14", ClientMessageID: "later", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	drained, _ := turnByID(h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		turn, found := turnByID(s, held.ID)
		return found && turn.State == domain.TurnStateRunning && turn.ProviderTurnID != ""
	}), held.ID)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: drained.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, held.ID)
	h.advance(time.Minute)
	sent := len(h.conv.sentMessages())
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentMessages(); len(got) != sent+1 {
		t.Fatalf("provider received %d messages, want the armed continue", len(got)-sent)
	}
}

func TestUsageLimitContinueQuotesTheUsersRefusedMessage(t *testing.T) {
	h := newHarness(t)
	human := failUsageLimitedTurn(t, h, timedUsageLimit)
	event := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if _, err := h.svc.RetryTurn(context.Background(), testSession, event.ID); err != nil {
		t.Fatal(err)
	}
	prompt, err := h.st.RetryPrompt(context.Background(), human.ConversationID, human.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertContinuedEvent(t, h.conv.sentMessages()[2], prompt.Text+`\n\nCI is failing on PR 13`)
}

func TestUsageLimitContinueKeepsEarlierAttachments(t *testing.T) {
	h := newHarness(t)
	caps := ports.ChatCapabilities{}
	caps[ports.ChatCapabilityImages] = true
	h.conv.setCapabilities(caps)
	ctx := context.Background()
	for _, image := range []string{"Zmlyc3Q=", "Zmlyc3Q=", "c2Vjb25k"} {
		human, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "match this mockup", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
			Content: []ports.ChatContent{{Type: "image", Data: image, MIMEType: "image/png"}}})
		if err != nil {
			t.Fatal(err)
		}
		h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: human.ProviderTurnID,
			TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
		failedTurnSnapshot(t, h, human.ID)
	}
	event := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if _, err := h.svc.RetryTurn(ctx, testSession, event.ID); err != nil {
		t.Fatal(err)
	}
	if sent := h.conv.sentMessages()[4]; len(sent.Content) != 2 || sent.Content[0].Data != "Zmlyc3Q=" || sent.Content[1].Data != "c2Vjb25k" {
		t.Fatalf("sent content = %+v, want each of the user's images once", sent.Content)
	}
}

func TestUsageLimitContinueQuotesTheUsersRefusedContinue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	task, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "refactor the auth module", ClientMessageID: "task", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	settleContinuationTestTurn(t, h, task, "", domain.TurnStateInterrupted)
	continued, err := h.svc.Send(ctx, testSession, continuationMessage("continue"))
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: continued.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, continued.ID)
	event := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if _, err := h.svc.RetryTurn(ctx, testSession, event.ID); err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentMessages()
	last := sent[len(sent)-1].Text
	for _, want := range []string{"refactor the auth module", "CI is failing on PR 13"} {
		if !strings.Contains(last, want) {
			t.Fatalf("sent = %q, want it to contain %q", last, want)
		}
	}
}

func TestUsageLimitContinueQuotesEarlierProgress(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "CI is failing on PR 12", ClientMessageID: "older", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: older.ProviderTurnID, ProviderItemID: "progress", Text: "steps 1-2 done"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: older.ProviderTurnID, TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)},
	)
	failedTurnSnapshot(t, h, older.ID)
	newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if _, err := h.svc.RetryTurn(ctx, testSession, newer.ID); err != nil {
		t.Fatal(err)
	}
	if sent := h.conv.sentMessages()[2].Text; !strings.Contains(sent, `CI is failing on PR 13","partialResponseTail":"steps 1-2 done"}`) {
		t.Fatalf("sent = %q, want the earlier event's progress", sent)
	}
}

func TestUsageLimitContinueBoundsAndDeduplicatesEvents(t *testing.T) {
	h := newHarness(t)
	failUsageLimitedEvent(t, h, "OLDEST", timedUsageLimit)
	failUsageLimitedEvent(t, h, strings.Repeat("a", 30*1024), timedUsageLimit)
	failUsageLimitedEvent(t, h, "OLDER", timedUsageLimit)
	var latest domain.ConversationTurn
	event := "CI is failing on PR 12" + strings.Repeat("b", 10*1024)
	for range 4 {
		latest = failUsageLimitedEvent(t, h, event, timedUsageLimit)
	}
	if _, err := h.svc.RetryTurn(context.Background(), testSession, latest.ID); err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentMessages()
	last := sent[len(sent)-1].Text
	if strings.Contains(last, "OLDEST") || !strings.Contains(last, "OLDER") || strings.Count(last, "CI is failing on PR 12") != 1 {
		t.Fatalf("sent %d bytes, want the newest events once each", len(last))
	}
}

func TestUsageLimitContinueQuotesALongEventInFull(t *testing.T) {
	h := newHarness(t)
	event := strings.Repeat("a", 20*1024) + "MIDDLE" + strings.Repeat("b", 20*1024)
	source := failUsageLimitedEvent(t, h, event, timedUsageLimit)
	if _, err := h.svc.RetryTurn(context.Background(), testSession, source.ID); err != nil {
		t.Fatal(err)
	}
	assertContinuedEvent(t, h.conv.sentMessages()[1], event)
}

func TestUsageLimitContinueQuotesTheLatestProgress(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	first, err := h.svc.RetryTurn(ctx, testSession, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: first.ProviderTurnID, ProviderItemID: "progress", Text: "step 2 done"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: first.ProviderTurnID, TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)},
	)
	failedTurnSnapshot(t, h, first.ID)
	if _, err := h.svc.RetryTurn(ctx, testSession, first.ID); err != nil {
		t.Fatal(err)
	}
	if last := h.conv.sentMessages()[2].Text; !strings.Contains(last, `{"originalRequest":"CI is failing on PR 12","partialResponseTail":"step 2 done"}`) ||
		strings.Count(last, "Continue from where you stopped.") != 1 {
		t.Fatalf("sent = %q, want the event and the progress since", last)
	}
}

func TestUsageLimitAutoResumeConsentDoesNotSpread(t *testing.T) {
	for name, tc := range map[string]struct {
		arm, human bool
		message    string
	}{
		"nothing armed":       {message: timedUsageLimit},
		"another failure":     {arm: true, message: "connection lost"},
		"armed human message": {arm: true, human: true, message: timedUsageLimit},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			var older domain.ConversationTurn
			if tc.human {
				older = failUsageLimitedTurn(t, h, timedUsageLimit)
			} else {
				older = failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
			}
			if tc.arm {
				if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			newer := failUsageLimitedEvent(t, h, "CI is failing on PR 13", tc.message)
			if newer.UsageLimit != nil && newer.UsageLimit.AutoResume {
				t.Fatal("newer event was armed")
			}
			if after, err := h.st.TurnByID(ctx, older.ID); err != nil || after.UsageLimit.AutoResume != tc.arm {
				t.Fatalf("older limit = %+v, %v, want armed %v", after.UsageLimit, err, tc.arm)
			}
		})
	}
}

func TestUsageLimitAutoResumeDoesNotMoveToAHumanMessage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	older := failUsageLimitedEvent(t, h, "CI is failing on PR 12", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, true); err != nil {
		t.Fatal(err)
	}
	if newer := failUsageLimitedTurn(t, h, timedUsageLimit); newer.UsageLimit.AutoResume {
		t.Fatal("the user's own message was armed without consent")
	}
	if err := h.svc.SetTurnAutoResume(ctx, testSession, older.ID, false); err != nil {
		t.Fatal(err)
	}
	event := failUsageLimitedEvent(t, h, "CI is failing on PR 13", timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, event.ID, true); err != nil {
		t.Fatal(err)
	}
	relayed, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "why this change?", ClientMessageID: "relayed", Origin: domain.MessageOriginAutomation, AuthoredByUser: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: relayed.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	if failed, _ := turnByID(failedTurnSnapshot(t, h, relayed.ID), relayed.ID); failed.UsageLimit.AutoResume {
		t.Fatal("a message the user relayed was armed without consent")
	}
}
