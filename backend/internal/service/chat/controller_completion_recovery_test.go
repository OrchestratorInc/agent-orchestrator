package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type completionRecoveryStore struct {
	Store
	read         func()
	fenced       bool
	pending      bool
	latestReads  atomic.Int32
	queueReads   atomic.Int32
	runningReads atomic.Int32
}

func (s *completionRecoveryStore) LatestPrimaryTurn(context.Context, string, string, string) (domain.ConversationTurn, bool, error) {
	s.latestReads.Add(1)
	if s.read != nil {
		s.read()
	}
	return domain.ConversationTurn{ID: "saved", ProviderTurnID: "native-saved", State: domain.TurnStateCompleted}, !s.fenced, nil
}

func (s *completionRecoveryStore) HasPendingConversationInteractions(context.Context, string) (bool, error) {
	return s.pending, nil
}

func (s *completionRecoveryStore) NextQueuedTurn(context.Context, string) (domain.QueuedTurn, error) {
	s.queueReads.Add(1)
	return domain.QueuedTurn{}, domain.ErrNoQueuedTurn
}

func (s *completionRecoveryStore) ListVisibleRunningTurnProviderIDs(context.Context, string) ([]string, error) {
	s.runningReads.Add(1)
	return nil, nil
}

type completionRecoveryActivity struct{ reports atomic.Int32 }

func (a *completionRecoveryActivity) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	a.reports.Add(1)
	return nil
}

type completionRecoveryConversation struct {
	ports.ChatConversation
	events chan ports.ChatEvent
	closed chan struct{}
	once   sync.Once
}

func (*completionRecoveryConversation) ProviderConversationID() string   { return "root" }
func (*completionRecoveryConversation) ReconnectedLive() bool            { return true }
func (*completionRecoveryConversation) PreservesProviderOnClose() bool   { return true }
func (c *completionRecoveryConversation) Events() <-chan ports.ChatEvent { return c.events }
func (c *completionRecoveryConversation) Close() error {
	c.once.Do(func() {
		close(c.closed)
		close(c.events)
	})
	return nil
}

func newCompletionRecoveryController() (*Controller, *completionRecoveryStore, *completionRecoveryActivity, *completionRecoveryConversation) {
	st := &completionRecoveryStore{}
	activity := &completionRecoveryActivity{}
	conv := &completionRecoveryConversation{events: make(chan ports.ChatEvent), closed: make(chan struct{})}
	c := &Controller{
		sessionID: "session", conversation: domain.ConversationRecord{ID: "conversation"}, generation: "current",
		harness: domain.HarnessCodex, conv: conv, store: st, activity: activity,
		state: ports.ChatControllerReady, stopped: make(chan struct{}), now: time.Now,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return c, st, activity, conv
}

func TestLiveCompletionRecoveryRequiresDurableAdmissionAndNoPendingInteraction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pending     bool
		fenced      bool
		wantEffects int32
	}{
		{name: "pending interaction", pending: true},
		{name: "replaced generation", fenced: true},
		{name: "saved completion with current ownership", wantEffects: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, st, activity, _ := newCompletionRecoveryController()
			st.pending, st.fenced = tc.pending, tc.fenced
			if err := c.reconcileLiveCompletion(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := st.latestReads.Load(); got != 1 {
				t.Fatalf("durable admission lookups = %d, want one", got)
			}
			if activity.reports.Load() != tc.wantEffects || st.queueReads.Load() != tc.wantEffects {
				t.Fatalf("recovery effects: activity=%d queue reads=%d, want %d each", activity.reports.Load(), st.queueReads.Load(), tc.wantEffects)
			}
		})
	}
}

func TestLiveCompletionRecoveryDoesNotDrainAfterCloseDuringDurableRead(t *testing.T) {
	c, st, activity, conv := newCompletionRecoveryController()
	reading, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Close(ctx)
	})
	st.read = func() { close(reading); <-release }
	c.start()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-reading:
	case <-ctx.Done():
		t.Fatal("startup did not begin its durable completion read")
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close(ctx) }()
	select {
	case <-conv.closed:
	case <-ctx.Done():
		t.Fatal("Close did not detach the native conversation")
	}
	unblock()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Close did not finish after the durable read resumed")
	}
	if activity.reports.Load() != 0 || st.queueReads.Load() != 0 || c.State() != ports.ChatControllerStopped {
		t.Fatalf("closed controller recovery: activity=%d queue reads=%d state=%s", activity.reports.Load(), st.queueReads.Load(), c.State())
	}
}

func TestLiveCompletionRecoveryDoesNotDrainWhenStopWaitsDuringDurableRead(t *testing.T) {
	c, st, activity, _ := newCompletionRecoveryController()
	reading, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Close(ctx)
	})
	st.read = func() { close(reading); <-release }
	c.start()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-reading:
	case <-ctx.Done():
		t.Fatal("startup did not begin its durable completion read")
	}
	interrupted := make(chan error, 1)
	go func() { interrupted <- c.Interrupt(ctx) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.Lock()
		stopping := c.interrupts != 0
		c.mu.Unlock()
		if stopping {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("Stop did not wait for startup's dispatch lock")
		}
	}
	unblock()
	select {
	case err := <-interrupted:
		if !errors.Is(err, ErrNoActiveTurn) {
			t.Fatalf("Stop error = %v, want no active native turn", err)
		}
	case <-ctx.Done():
		t.Fatal("Stop did not finish after the durable read resumed")
	}
	if activity.reports.Load() != 0 || st.queueReads.Load() != 0 || st.runningReads.Load() != 1 {
		t.Fatalf("stopped controller recovery: activity=%d queue reads=%d native turn checks=%d", activity.reports.Load(), st.queueReads.Load(), st.runningReads.Load())
	}
}
