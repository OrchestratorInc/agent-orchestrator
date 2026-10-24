package zcodeappserver

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The pump emits turn.started on its own goroutine after observeStarted returns,
// so a session/send failure that lands in that window must not emit its failed
// completion first: the controller would ignore the completion and then adopt
// the late turn.started with nothing to settle it.
func TestCancelAfterStartOrdersTheFailureAfterTurnStarted(t *testing.T) {
	failure := ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "minted-1", TurnState: domain.TurnStateFailed}

	t.Run("turn.started not yet on the stream", func(t *testing.T) {
		tracker := newTurnTracker()
		tracker.prepare("minted-1")
		if ev := tracker.observeStarted("", ""); ev == nil {
			t.Fatal("observeStarted emitted nothing")
		}
		if tracker.cancel("minted-1", failure) {
			t.Fatal("caller told to emit the failure ahead of turn.started")
		}
		if tracker.active() != "" {
			t.Fatalf("active = %q after cancel", tracker.active())
		}
		got := tracker.markStartedEmitted("minted-1")
		if got == nil || got.ProviderTurnID != "minted-1" || got.TurnState != domain.TurnStateFailed {
			t.Fatalf("pump was not handed the failure after turn.started: %+v", got)
		}
		if tracker.markStartedEmitted("minted-1") != nil {
			t.Fatal("failure handed out twice")
		}
	})

	t.Run("turn.started already on the stream", func(t *testing.T) {
		tracker := newTurnTracker()
		tracker.prepare("minted-1")
		tracker.observeStarted("t-native", "")
		if tracker.markStartedEmitted("minted-1") != nil {
			t.Fatal("nothing should be pending before a cancel")
		}
		if !tracker.cancel("minted-1", failure) {
			t.Fatal("caller must emit the failure itself once turn.started is out")
		}
		if tracker.markStartedEmitted("minted-1") != nil {
			t.Fatal("failure parked although the caller emitted it")
		}
	})

	t.Run("never started", func(t *testing.T) {
		tracker := newTurnTracker()
		tracker.prepare("minted-1")
		if tracker.cancel("minted-1", failure) {
			t.Fatal("a turn that never started has nothing to settle")
		}
		if tracker.active() != "" || tracker.markStartedEmitted("minted-1") != nil {
			t.Fatal("cancel left state behind")
		}
	})
}

func TestAdoptCorrelatesWithoutEmitting(t *testing.T) {
	tracker := newTurnTracker()
	tracker.adopt("durable", "t-native")
	if tracker.active() != "durable" || tracker.mintedFor("t-native") != "durable" || tracker.nativeFor("durable") != "t-native" {
		t.Fatal("adopted turn not correlated")
	}
	if ev := tracker.observeStarted("t-native", ""); ev != nil {
		t.Fatalf("a start signal for the adopted turn emitted %+v", ev)
	}
	completed := tracker.observeCompleted(domain.TurnStateCompleted, nil)
	if completed == nil || completed.ProviderTurnID != "durable" || completed.NativeTurnID != "t-native" {
		t.Fatalf("turn.completed = %+v", completed)
	}
	// With no native id from the snapshot, the completion still settles the
	// adopted turn through the open-turn fallback.
	tracker.adopt("durable-2", "")
	if turnFor(tracker, "t-unknown") != "durable-2" {
		t.Fatalf("turnFor = %q, want the adopted turn", turnFor(tracker, "t-unknown"))
	}
}
