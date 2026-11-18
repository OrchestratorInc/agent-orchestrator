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

// idleOrchestrator starts a compacting provider whose session reports the
// given context size, kind and activity.
func idleOrchestrator(
	t *testing.T,
	contextUsed int64,
	kind domain.SessionKind,
	state domain.ActivityState,
	idle time.Duration,
) (*harness, *compactingConversation) {
	t.Helper()
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
		ContextUsed: contextUsed, ContextWindow: 1_000_000, ContextKnown: true,
	}})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.ContextUsed == contextUsed
	})
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Kind = kind
	rec.Activity = domain.Activity{State: state, LastActivityAt: h.now().Add(-idle)}
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	return h, conv
}

// A large orchestrator that has gone quiet is compacted while its prompt cache
// is still warm, and only once for that quiet period.
func TestCompactIdleOrchestratorCompactsOncePerIdlePeriod(t *testing.T) {
	h, conv := idleOrchestrator(t, 400_000, domain.KindOrchestrator, domain.ActivityIdle, 50*time.Minute)
	ctx := context.Background()

	compacted, err := h.svc.CompactIdleOrchestrator(ctx, testSession)
	if err != nil || !compacted || conv.compactCalls() != 1 {
		t.Fatalf("first pass: compacted=%v err=%v calls=%d, want one compaction", compacted, err, conv.compactCalls())
	}
	// The next sweep finds the same quiet period: a second compaction would be
	// a wasted summary turn.
	if err := h.svc.CompactIdleOrchestrators(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d after a repeat pass, want 1", conv.compactCalls())
	}
}

func TestCompactIdleOrchestratorSkipsIneligibleSessions(t *testing.T) {
	tests := []struct {
		name        string
		contextUsed int64
		kind        domain.SessionKind
		state       domain.ActivityState
		idle        time.Duration
	}{
		// Workers can lose mid-task code context, and many never wake again.
		{name: "worker", contextUsed: 400_000, kind: domain.KindWorker, state: domain.ActivityIdle, idle: 50 * time.Minute},
		{name: "not idle long enough", contextUsed: 400_000, kind: domain.KindOrchestrator, state: domain.ActivityIdle, idle: chatsvc.IdleBeforeCompaction - time.Minute},
		// After the cache has expired, compacting pays the full re-write itself.
		{name: "cache already expired", contextUsed: 400_000, kind: domain.KindOrchestrator, state: domain.ActivityIdle, idle: chatsvc.IdleCompactionDeadline},
		{name: "small context", contextUsed: chatsvc.IdleCompactionMinContext - 1, kind: domain.KindOrchestrator, state: domain.ActivityIdle, idle: 50 * time.Minute},
		{name: "working", contextUsed: 400_000, kind: domain.KindOrchestrator, state: domain.ActivityActive, idle: 50 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, conv := idleOrchestrator(t, tt.contextUsed, tt.kind, tt.state, tt.idle)
			compacted, err := h.svc.CompactIdleOrchestrator(context.Background(), testSession)
			if err != nil || compacted || conv.compactCalls() != 0 {
				t.Fatalf("compacted=%v err=%v calls=%d, want no compaction", compacted, err, conv.compactCalls())
			}
		})
	}
}

// A provider that cannot compact is left alone rather than failing every sweep.
func TestCompactIdleOrchestratorSkipsProvidersWithoutCompaction(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Kind = domain.KindOrchestrator
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: h.now().Add(-50 * time.Minute)}
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if compacted, err := h.svc.CompactIdleOrchestrator(ctx, testSession); err != nil || compacted {
		t.Fatalf("compacted=%v err=%v, want skipped", compacted, err)
	}
}
