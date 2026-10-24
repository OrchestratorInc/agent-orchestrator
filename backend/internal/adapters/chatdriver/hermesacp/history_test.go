package hermesacp

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReconcileReplayDropsFailedTurnNoticesOnly(t *testing.T) {
	notice := hermesFailedTurnNotices[0]
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "turn-1"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "turn-1", Text: "What is X?"},
		{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-1", ProviderItemID: "assistant-turn-1", Delta: "Answer "},
		{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-1", ProviderItemID: "assistant-turn-1", Delta: "one."},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn-1", ProviderItemID: "assistant-turn-1", Text: "Answer one."},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "turn-1"},
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "turn-2"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "turn-2", Text: "Do turn two."},
		{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-2", ProviderItemID: "assistant-turn-2", Delta: notice[:20]},
		{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-2", ProviderItemID: "assistant-turn-2", Delta: notice[20:]},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn-2", ProviderItemID: "assistant-turn-2", Text: "\n  " + notice + "\n"},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "turn-2"},
	}
	got := reconcileReplay(events)
	// The chat service rebuilds a reply from the replay's message.delta chunks,
	// so every event carrying the notice item's provider id must go — not just
	// the completed event (review comment 4365736419 on PR #6635).
	if len(got) != 9 {
		t.Fatalf("reconciled events = %d, want 9 (turn-1 reply plus turn-2's user message and outcome)", len(got))
	}
	for _, event := range got {
		if event.ProviderItemID == "assistant-turn-2" {
			t.Fatalf("event %+v still carries the notice item", event)
		}
		if event.Kind == ports.ChatEventMessageDelta && isFailedTurnNotice(event.Delta) {
			t.Fatalf("delta %q carries the boundary notice", event.Delta)
		}
		if event.Kind == ports.ChatEventMessageCompleted && event.ProviderTurnID == "turn-2" {
			t.Fatal("boundary notice survived reconciliation as a reply")
		}
	}
	// Turn-1's reply survives whole: both delta chunks and the completed event.
	if got[2].Kind != ports.ChatEventMessageDelta || got[2].Delta != "Answer " ||
		got[3].Kind != ports.ChatEventMessageDelta || got[3].Delta != "one." ||
		got[4].Kind != ports.ChatEventMessageCompleted || got[4].Text != "Answer one." {
		t.Fatalf("turn-1 reply events = %+v, %+v, %+v, want the untouched reply", got[2], got[3], got[4])
	}
	// The stored copy is untouched: reconciliation runs after the caller
	// snapshot, and the input here is that fresh copy.
	if len(events) != 12 {
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

// A notice whose completed event carries no provider item id still drops; the
// id-based sweep is an optimization over the text match, never a dependency.
func TestReconcileReplayDropsNoticeWithoutItemID(t *testing.T) {
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "turn-1", Text: "Do turn two."},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn-1", Text: hermesFailedTurnNotices[1]},
	}
	got := reconcileReplay(events)
	if len(got) != 1 || got[0].Kind != ports.ChatEventUserMessageCompleted {
		t.Fatalf("reconciled events = %+v, want only the user message", got)
	}
}
