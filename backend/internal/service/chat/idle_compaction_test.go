package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// coldCache is a 500K-token conversation whose one-hour cache expired an hour
// ago, so the next request would reprocess all of it at full price.
func coldCache(h *harness) ports.PromptCacheState {
	return ports.PromptCacheState{LastRequestAt: h.now().Add(-2 * time.Hour), TTL: time.Hour, ContextTokens: 500_000}
}

func TestMessageToColdChatCompactsFirstThenSends(t *testing.T) {
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	h.ctrl.SetIdleCompactionState(domain.IdleCompactionOnReturn, coldCache(h))
	ctx := context.Background()

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "back again", Origin: domain.MessageOriginHuman})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("Send to a cold chat = %+v, %v; want it queued behind the compaction", turn, err)
	}
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1", conv.compactCalls())
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	awaitSent(t, conv.fakeConversation, "back again")
	// The transcript still reports the same cold request, but one idle spell
	// compacts once: the drain must not compact again ahead of the message.
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions after drain = %d, want 1", conv.compactCalls())
	}
}

func TestFailedIdleCompactionStillSendsTheMessage(t *testing.T) {
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	h.ctrl.SetIdleCompactionState(domain.IdleCompactionOnReturn, coldCache(h))
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "still here", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateFailed})
	awaitSent(t, conv.fakeConversation, "still here")
}

func TestIdleCompactionLeavesWarmSmallAndOffChatsAlone(t *testing.T) {
	cases := map[string]func(*harness) (domain.IdleCompactionMode, ports.PromptCacheState){
		"warm cache": func(h *harness) (domain.IdleCompactionMode, ports.PromptCacheState) {
			return domain.IdleCompactionOnReturn, ports.PromptCacheState{LastRequestAt: h.now().Add(-10 * time.Minute), TTL: time.Hour, ContextTokens: 500_000}
		},
		"small context": func(h *harness) (domain.IdleCompactionMode, ports.PromptCacheState) {
			return domain.IdleCompactionOnReturn, ports.PromptCacheState{LastRequestAt: h.now().Add(-2 * time.Hour), TTL: time.Hour, ContextTokens: 150_000}
		},
		"off": func(h *harness) (domain.IdleCompactionMode, ports.PromptCacheState) {
			return domain.IdleCompactionOff, coldCache(h)
		},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			conv := newCompactingConversation()
			h := newHarnessWithConversation(t, conv)
			h.ctrl.SetIdleCompactionState(state(h))
			turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "hello", Origin: domain.MessageOriginHuman})
			if err != nil || turn.State == domain.TurnStateQueued {
				t.Fatalf("Send = %+v, %v; want it dispatched at once", turn, err)
			}
			if conv.compactCalls() != 0 {
				t.Fatalf("compactions = %d, want 0", conv.compactCalls())
			}
		})
	}
}

func TestTypedCompactIsNotPrecededByAnother(t *testing.T) {
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	h.ctrl.SetIdleCompactionState(domain.IdleCompactionOnReturn, coldCache(h))
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "/compact keep the API notes", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	if conv.compactCalls() != 0 {
		t.Fatalf("compactions = %d, want 0 ahead of a typed /compact", conv.compactCalls())
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
}

func TestIdleCompactionDecisions(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	big := int64(domain.IdleCompactionMinContextTokens)
	due := []struct {
		name  string
		state ports.PromptCacheState
		want  bool
	}{
		{"expired hour cache", ports.PromptCacheState{LastRequestAt: now.Add(-61 * time.Minute), TTL: time.Hour, ContextTokens: big}, true},
		{"live hour cache", ports.PromptCacheState{LastRequestAt: now.Add(-59 * time.Minute), TTL: time.Hour, ContextTokens: big}, false},
		{"expired five-minute cache", ports.PromptCacheState{LastRequestAt: now.Add(-6 * time.Minute), TTL: 5 * time.Minute, ContextTokens: big}, true},
		{"unknown lifetime inside fallback", ports.PromptCacheState{LastRequestAt: now.Add(-65 * time.Minute), ContextTokens: big}, false},
		{"unknown lifetime past fallback", ports.PromptCacheState{LastRequestAt: now.Add(-71 * time.Minute), ContextTokens: big}, true},
		{"below threshold", ports.PromptCacheState{LastRequestAt: now.Add(-3 * time.Hour), TTL: time.Hour, ContextTokens: big - 1}, false},
		{"no request", ports.PromptCacheState{ContextTokens: big}, false},
	}
	for _, tc := range due {
		if got := chatsvc.IdleCompactionDue(tc.state, now); got != tc.want {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.want)
		}
	}

	last := now.Add(-50 * time.Minute)
	start, end, ok := chatsvc.IdleCompactionWindow(ports.PromptCacheState{LastRequestAt: last, TTL: time.Hour, ContextTokens: big})
	if !ok || !end.Equal(last.Add(time.Hour)) || !start.Equal(end.Add(-domain.IdleCompactionLead)) {
		t.Errorf("window = %v..%v ok=%v", start, end, ok)
	}
	if _, _, ok := chatsvc.IdleCompactionWindow(ports.PromptCacheState{LastRequestAt: last, ContextTokens: big}); ok {
		t.Error("an unknown lifetime must not arm a timer: compacting after expiry pays the full-price read")
	}

	for text, want := range map[string]bool{
		"/compact": true, " /COMPACT ": true, "/compact keep notes": true,
		"/compacting": false, "please /compact": false, "": false,
	} {
		if got := chatsvc.IsCompactCommand(text); got != want {
			t.Errorf("IsCompactCommand(%q) = %v, want %v", text, got, want)
		}
	}
}
