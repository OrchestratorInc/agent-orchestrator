package usage

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

type blockingHookRecorder struct {
	release chan struct{}
	mu      sync.Mutex
	events  []string
}

func (r *blockingHookRecorder) RecordHook(_ context.Context, _ domain.SessionID, signal HookSignal) error {
	<-r.release
	r.mu.Lock()
	r.events = append(r.events, signal.Event)
	r.mu.Unlock()
	return nil
}

func (r *blockingHookRecorder) FinalizeSession(context.Context, domain.SessionID, string, int64) error {
	r.mu.Lock()
	r.events = append(r.events, "finalize")
	r.mu.Unlock()
	return nil
}

func (r *blockingHookRecorder) ReactivateSession(context.Context, domain.SessionID, string) error {
	return nil
}

func (r *blockingHookRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func TestHookQueueReturnsWhileRecorderIsBusyAndKeepsOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := &blockingHookRecorder{release: make(chan struct{})}
	queue := NewHookQueue(recorder, nil)
	go queue.Run(ctx)

	want := []string{"session-start", "user-prompt-submit", "pre-tool-use", "post-tool-use", "stop"}
	start := time.Now()
	for _, event := range want {
		if err := queue.RecordHook(ctx, "s-1", HookSignal{Event: event}); err != nil {
			t.Fatalf("RecordHook(%s): %v", event, err)
		}
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RecordHook waited %s for a busy recorder", elapsed)
	}

	close(recorder.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		recorder.mu.Lock()
		got := append([]string(nil), recorder.events...)
		recorder.mu.Unlock()
		if len(got) == len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("recorded order %v, want %v", got, want)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHookQueueFinalizeRecordsQueuedSignalsFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := &blockingHookRecorder{release: make(chan struct{})}
	queue := NewHookQueue(recorder, nil)
	go queue.Run(ctx)

	for _, event := range []string{"session-start", "user-prompt-submit"} {
		if err := queue.RecordHook(ctx, "s-1", HookSignal{Event: event}); err != nil {
			t.Fatalf("RecordHook(%s): %v", event, err)
		}
	}
	finalized := make(chan error, 1)
	go func() { finalized <- queue.FinalizeSession(ctx, "s-1", "launch-1", 7) }()
	select {
	case err := <-finalized:
		t.Fatalf("FinalizeSession returned before the queued signals were recorded: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(recorder.release)
	select {
	case err := <-finalized:
		if err != nil {
			t.Fatalf("FinalizeSession: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FinalizeSession did not return")
	}
	if got, want := recorder.recorded(), []string{"session-start", "user-prompt-submit", "finalize"}; !slices.Equal(got, want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
}

func TestHookQueueFinalizeDoesNotWaitAfterRunStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &blockingHookRecorder{release: make(chan struct{})}
	queue := NewHookQueue(recorder, nil)
	stopped := make(chan struct{})
	go func() {
		queue.Run(ctx)
		close(stopped)
	}()
	cancel()
	<-stopped

	finalized := make(chan error, 1)
	go func() { finalized <- queue.FinalizeSession(context.Background(), "s-1", "launch-1", 7) }()
	select {
	case err := <-finalized:
		if err != nil {
			t.Fatalf("FinalizeSession: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FinalizeSession waited for a stopped queue")
	}
	if got, want := recorder.recorded(), []string{"finalize"}; !slices.Equal(got, want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
}
