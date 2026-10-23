package hermesacp

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReconcileReplayDropsFailedTurnNoticesOnly(t *testing.T) {
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "turn-1"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "turn-1", Text: "What is X?"},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn-1", Text: "Answer one."},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "turn-1"},
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "turn-2"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "turn-2", Text: "Do turn two."},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn-2", Text: "\n  " + hermesFailedTurnNotices[0] + "\n"},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "turn-2"},
	}
	got := reconcileReplay(events)
	if len(got) != len(events)-1 {
		t.Fatalf("reconciled events = %d, want %d", len(got), len(events)-1)
	}
	for _, event := range got {
		if event.Kind == ports.ChatEventMessageCompleted && event.ProviderTurnID == "turn-2" {
			t.Fatal("boundary notice survived reconciliation as a reply")
		}
	}
	// The stored copy is untouched: reconciliation runs after the caller
	// snapshot, and the input here is that fresh copy.
	if len(events) != 8 {
		t.Fatalf("input slice length changed to %d", len(events))
	}

	for i, notice := range hermesFailedTurnNotices {
		if !isFailedTurnNotice(" " + notice + "\n") {
			t.Fatalf("notice %d not recognized", i)
		}
	}
	if isFailedTurnNotice("Your request was not processed. Send it again if you still want me to carry it out..") ||
		isFailedTurnNotice("") ||
		isFailedTurnNotice("Your request was not processed. Send it again if you still want me to carry it out. (context)") {
		t.Fatal("non-boundary text matched a notice")
	}
}
