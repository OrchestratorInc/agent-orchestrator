package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeCheckpointRequiresExactUserBoundaryAndAnswer(t *testing.T) {
	checkpoint := nativeHistoryCheckpoint{nativeBoundary: &ports.NativeCheckpointBoundary{
		UserMessageID: "native-B", UserText: "continue", AssistantText: "Done",
	}}
	replay := func(nativeID, answer string) []ports.ChatEvent {
		return []ports.ChatEvent{
			{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "reconstructed-B", NativeUserMessageID: nativeID, Text: "continue"},
			{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "reconstructed-B", Text: answer},
			{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "reconstructed-B"},
		}
	}
	for _, test := range []struct {
		name, id, answer string
		wantPass         bool
	}{
		{"exact", "native-B", "Done", true},
		{"repeated earlier turn", "native-A", "Done", false},
		{"synthetic identity", "", "Done", false},
		{"missing answer", "native-B", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := checkpoint.mismatches(replay(test.id, test.answer), nil, nil, nil)
			if (len(got) == 0) != test.wantPass {
				t.Fatalf("mismatches=%v", got)
			}
		})
	}
}

// An Esc-interrupted boundary turn has no answer. What the replay shows for it
// (a partial reply, nothing, or Claude's "No response requested." filler once
// the conversation resumes) depends on timing, so only its identity is matched.
func TestNativeCheckpointInterruptedBoundaryMatchesWithoutAnswer(t *testing.T) {
	checkpoint := nativeHistoryCheckpoint{nativeBoundary: &ports.NativeCheckpointBoundary{
		UserMessageID: "native-A", UserText: "run it", Interrupted: true,
	}}
	replay := func(nativeID, user, answer string, completed bool) []ports.ChatEvent {
		events := []ports.ChatEvent{
			{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "reconstructed-A", NativeUserMessageID: nativeID, Text: user},
		}
		if answer != "" {
			events = append(events, ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "reconstructed-A", Text: answer})
		}
		if completed {
			events = append(events, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "reconstructed-A"})
		}
		return events
	}
	for _, test := range []struct {
		name, id, user, answer string
		completed, wantPass    bool
	}{
		{"no answer", "native-A", "run it", "", true, true},
		{"partial answer", "native-A", "run it", "tick 0 tick 1", true, true},
		{"resume filler", "native-A", "run it", "No response requested.", true, true},
		{"other native user", "native-B", "run it", "", true, false},
		{"other user text", "native-A", "something else", "", true, false},
		{"turn not settled in replay", "native-A", "run it", "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := checkpoint.mismatches(replay(test.id, test.user, test.answer, test.completed), nil, nil, nil)
			if (len(got) == 0) != test.wantPass {
				t.Fatalf("mismatches=%v", got)
			}
		})
	}
}

// Codex records an Esc-interrupted terminal turn as an interrupted turn in its
// thread, and AO's checkpoint keeps that turn's prompt (no Stop fires). The
// replayed interrupted turn settles the checkpoint, before and after a later
// completed turn, so Codex has no Claude-style unsettled interrupt.
func TestNativeCheckpointCodexInterruptedTurnSettles(t *testing.T) {
	turn := func(id, user, answer string, state domain.TurnState) []ports.ChatEvent {
		events := []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: id, Text: user},
		}
		if answer != "" {
			events = append(events, ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: id, Text: answer})
		}
		return append(events, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id, TurnState: state})
	}
	interruptedPrompt := nativeHistoryCheckpoint{
		providerTurnID: "turn-1", latestUserPrompt: "run it", userMismatch: ports.ChatHistoryMismatchTrustedUserText,
	}
	if got := interruptedPrompt.mismatches(turn("turn-1", "run it", "", domain.TurnStateInterrupted), nil, nil, nil); len(got) != 0 {
		t.Fatalf("interrupted latest turn: mismatches=%v", got)
	}
	later := nativeHistoryCheckpoint{
		providerTurnID: "turn-2", latestUserPrompt: "say hi", latestAssistantUpdate: "hi", completedUserPrompt: true,
		userMismatch: ports.ChatHistoryMismatchTrustedUserText, assistantMismatch: ports.ChatHistoryMismatchTrustedAssistantText,
	}
	replay := append(turn("turn-1", "run it", "", domain.TurnStateInterrupted), turn("turn-2", "say hi", "hi", domain.TurnStateCompleted)...)
	if got := later.mismatches(replay, nil, nil, nil); len(got) != 0 {
		t.Fatalf("interrupt, then a completed turn: mismatches=%v", got)
	}
	// A replay missing the interrupted turn still fails its checkpoint.
	if got := interruptedPrompt.mismatches(turn("turn-0", "earlier", "done", domain.TurnStateCompleted), nil, nil, nil); len(got) == 0 {
		t.Fatal("admitted a replay without the checkpoint's turn")
	}
}
