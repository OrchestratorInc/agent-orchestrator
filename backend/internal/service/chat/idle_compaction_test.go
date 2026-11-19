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

const (
	oneHourCacheWrite  = `{"cache_creation":{"ephemeral_1h_input_tokens":4000,"ephemeral_5m_input_tokens":0}}`
	fiveMinCacheWrite  = `{"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":4000}}`
	cacheReadOnlyUsage = `{"cache_read_input_tokens":400000,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}}`
)

// idleOrchestratorSetup describes the orchestrator a test starts. Its zero
// value, after defaults, is one that idle compaction should compact.
type idleOrchestratorSetup struct {
	contextUsed int64
	kind        domain.SessionKind
	state       domain.ActivityState
	idle        time.Duration
	// optedOut leaves the project setting at its default, off.
	optedOut bool
	// cacheUsage is the provider usage of the transcript's newest record;
	// empty records no usage at all.
	cacheUsage string
	turnState  domain.TurnState
	// compactedAfterTurn records a compaction after the latest turn.
	compactedAfterTurn bool
	// pendingApproval leaves an approval waiting on a person.
	pendingApproval bool
}

func defaultIdleOrchestrator() idleOrchestratorSetup {
	return idleOrchestratorSetup{
		contextUsed: 400_000,
		kind:        domain.KindOrchestrator,
		state:       domain.ActivityIdle,
		idle:        50 * time.Minute,
		cacheUsage:  oneHourCacheWrite,
		turnState:   domain.TurnStateCompleted,
	}
}

// idleOrchestrator starts a compacting provider whose session matches setup:
// one settled turn, a reported context size, the project setting, and the
// prompt cache tier its transcript recorded.
func idleOrchestrator(t *testing.T, setup idleOrchestratorSetup) (*harness, *compactingConversation) {
	t.Helper()
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "plan the work", ClientMessageID: "idle-1"}); err != nil {
		t.Fatal(err)
	}
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: setup.turnState},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == setup.turnState
	})
	if err := h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if setup.pendingApproval {
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "approval", RequestID: "approval", Summary: "Approve command",
			Decisions: []ports.ChatDecisionOption{{ID: "allow", Label: "Allow", Kind: ports.ChatDecisionAllowOnce}}})
		h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
			return len(s.Activities) != 0 && s.Activities[len(s.Activities)-1].Status == domain.ActivityStatusPending
		})
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
		ContextUsed: setup.contextUsed, ContextWindow: 1_000_000, ContextKnown: true,
	}})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.ContextUsed == setup.contextUsed
	})
	if setup.compactedAfterTurn {
		if err := h.st.MarkCompacted(ctx, h.ctrl.ConversationID(), h.now()); err != nil {
			t.Fatal(err)
		}
	}

	project, found, err := h.st.GetProject(ctx, string(testProject))
	if err != nil || !found {
		t.Fatalf("get project = %v, %v", found, err)
	}
	project.Config.IdleOrchestratorCompaction = !setup.optedOut
	if err := h.st.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if setup.cacheUsage != "" {
		recordTranscriptUsage(t, h, setup.cacheUsage)
	}

	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Kind = setup.kind
	rec.Activity = domain.Activity{State: setup.state, LastActivityAt: h.now().Add(-setup.idle)}
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	return h, conv
}

// recordTranscriptUsage stores one usage record from the session's main Claude
// transcript, as usage ingestion would.
func recordTranscriptUsage(t *testing.T, h *harness, providerUsage string) {
	t.Helper()
	ctx := context.Background()
	now := h.now()
	binding, err := h.st.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID: testSession, Harness: domain.HarnessClaudeCode, NativeRootID: "native-root",
		State: domain.UsageBindingActive, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := h.st.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID: binding.ID, Kind: domain.UsageSourceClaudeMain, NativeSessionID: "native-root",
		ArtifactPath: "/tmp/claude/transcript.jsonl", FileIdentity: "dev:ino",
		State: domain.UsageSourcePending, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
		ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now,
	}, []domain.ModelUsageEvent{{
		ProviderID: domain.UsageProviderAnthropic, BillingProviderID: "anthropic",
		BillingProviderSource: domain.UsageBillingProviderObserved, ModelID: "claude-x",
		MeasurementKind: domain.UsageMeasurementNativeReported, ProviderUsageJSON: providerUsage,
		CreatedAt: now.Add(-50 * time.Minute), SourceEventKey: "turn-1",
	}}); err != nil {
		t.Fatal(err)
	}
}

// A large orchestrator that has gone quiet is compacted while its prompt cache
// is still warm, and only once for that quiet period.
func TestCompactIdleOrchestratorCompactsOncePerIdlePeriod(t *testing.T) {
	h, conv := idleOrchestrator(t, defaultIdleOrchestrator())
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
		name   string
		change func(*idleOrchestratorSetup)
	}{
		// Workers can lose mid-task code context, and many never wake again.
		{name: "worker", change: func(s *idleOrchestratorSetup) { s.kind = domain.KindWorker }},
		{name: "not idle long enough", change: func(s *idleOrchestratorSetup) { s.idle = chatsvc.IdleBeforeCompaction - time.Minute }},
		// After the cache has expired, compacting pays the full re-write itself.
		{name: "cache already expired", change: func(s *idleOrchestratorSetup) { s.idle = chatsvc.IdleCompactionDeadline }},
		{name: "small context", change: func(s *idleOrchestratorSetup) { s.contextUsed = chatsvc.IdleCompactionMinContext - 1 }},
		{name: "working", change: func(s *idleOrchestratorSetup) { s.state = domain.ActivityActive }},
		{name: "project has not opted in", change: func(s *idleOrchestratorSetup) { s.optedOut = true }},
		// A five-minute cache expired long before the idle window opens.
		{name: "five-minute cache", change: func(s *idleOrchestratorSetup) { s.cacheUsage = fiveMinCacheWrite }},
		{name: "cache lifetime not recorded", change: func(s *idleOrchestratorSetup) { s.cacheUsage = "" }},
		{name: "only cache reads recorded", change: func(s *idleOrchestratorSetup) { s.cacheUsage = cacheReadOnlyUsage }},
		{name: "latest turn failed", change: func(s *idleOrchestratorSetup) { s.turnState = domain.TurnStateFailed }},
		{name: "latest turn interrupted", change: func(s *idleOrchestratorSetup) { s.turnState = domain.TurnStateInterrupted }},
		{name: "already compacted since the latest turn", change: func(s *idleOrchestratorSetup) { s.compactedAfterTurn = true }},
		{name: "approval waiting on a person", change: func(s *idleOrchestratorSetup) { s.pendingApproval = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setup := defaultIdleOrchestrator()
			tt.change(&setup)
			h, conv := idleOrchestrator(t, setup)
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
