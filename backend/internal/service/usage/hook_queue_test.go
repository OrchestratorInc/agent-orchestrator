package usage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
