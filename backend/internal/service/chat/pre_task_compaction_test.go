package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

const preTaskOrchestrator = "orch-1"

// orchestratorTasksOver is a compact-before-task rule that applies to tasks from
// preTaskOrchestrator over threshold tokens.
func orchestratorTasksOver(threshold int64) chatsvc.PreTaskCompactionPolicy {
	return func(_ context.Context, sender domain.SessionID) (int64, bool) {
		return threshold, sender == preTaskOrchestrator
	}
}

// reportContext makes the provider report a context size and waits until the
// conversation records it.
func reportContext(t *testing.T, h *harness, conv *fakeConversation, used int64) {
	t.Helper()
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
		ContextUsed: used, ContextWindow: 1_000_000, ContextKnown: true,
	}})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.ContextUsed == used
	})
}

func orchestratorTask(text string) ports.ChatUserMessage {
	return ports.ChatUserMessage{Text: text, SenderSessionID: preTaskOrchestrator, Origin: domain.MessageOriginAutomation}
}

// awaitProviderTexts waits until the provider has received want, in order.
func awaitProviderTexts(t *testing.T, conv *fakeConversation, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got := conv.sentTexts()
		if len(got) == len(want) {
			match := true
			for i := range want {
				if got[i] != want[i] {
					match = false
				}
			}
			if match {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider messages = %v, want %v", conv.sentTexts(), want)
}

func newPreTaskHarness(t *testing.T, threshold int64) (*harness, *compactingConversation) {
	t.Helper()
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	h.ctrl.SetPreTaskCompactionPolicy(orchestratorTasksOver(threshold))
	return h, conv
}

// The measured case behind #8385: a worker carrying 463k tokens of history got a
// new task, and every response to it re-read all of that. The task now waits for
// one compaction and runs on the compacted context.
func TestPreTaskCompactionCompactsBeforeAnOrchestratorTask(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	ctx := context.Background()
	reportContext(t, h, conv.fakeConversation, 463_550)

	turn, err := h.svc.Send(ctx, testSession, orchestratorTask("next task"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.State != domain.TurnStateQueued {
		t.Fatalf("task state = %q, want queued behind the compaction", turn.State)
	}
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1", conv.compactCalls())
	}
	if got := conv.sentTexts(); len(got) != 0 {
		t.Fatalf("task reached the provider before the compaction settled: %v", got)
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	awaitProviderTexts(t, conv.fakeConversation, "next task")
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want exactly 1", conv.compactCalls())
	}
}

// A failed turn normally holds the queue so it cannot cascade through an outage
// (#4861). A failed compaction must not: without it the task would already have
// run, so compaction can only ever delay the work, never strand it.
func TestPreTaskCompactionFailureStillDeliversTheTask(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	ctx := context.Background()
	reportContext(t, h, conv.fakeConversation, 463_550)

	if _, err := h.svc.Send(ctx, testSession, orchestratorTask("next task")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn",
		TurnState: domain.TurnStateFailed, Err: errors.New("compaction failed"),
	})
	awaitProviderTexts(t, conv.fakeConversation, "next task")
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1 and no retry", conv.compactCalls())
	}
}

// A compaction that leaves the context above the threshold must not start
// another before the same task; the guard is per task, not per context size.
func TestPreTaskCompactionRunsOncePerTask(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	ctx := context.Background()
	reportContext(t, h, conv.fakeConversation, 463_550)

	if _, err := h.svc.Send(ctx, testSession, orchestratorTask("next task")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The compaction barely shrinks the history.
	reportContext(t, h, conv.fakeConversation, 450_000)
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	awaitProviderTexts(t, conv.fakeConversation, "next task")
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1", conv.compactCalls())
	}
}

// A task queued behind running work is checked when the queue drains, which is
// also the path a hibernated worker's queued task takes when it wakes.
func TestPreTaskCompactionBeforeAQueuedTask(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "current work", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	awaitProviderTexts(t, conv.fakeConversation, "current work")
	reportContext(t, h, conv.fakeConversation, 463_550)

	turn, err := h.svc.Send(ctx, testSession, orchestratorTask("next task"))
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("Send while busy = %+v, %v", turn, err)
	}
	if conv.compactCalls() != 0 {
		t.Fatal("compacted while a turn was running; the running work would have been lost")
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	deadline := time.Now().Add(5 * time.Second)
	for conv.compactCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1 once the running turn ended", conv.compactCalls())
	}
	if got := conv.sentTexts(); len(got) != 1 {
		t.Fatalf("queued task dispatched before its compaction settled: %v", got)
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	awaitProviderTexts(t, conv.fakeConversation, "current work", "next task")
}

// Cases the rule leaves alone deliver the task at once, without a compaction.
func TestPreTaskCompactionLeavesOtherTurnsAlone(t *testing.T) {
	tests := []struct {
		name    string
		context int64
		msg     ports.ChatUserMessage
	}{
		{"under the threshold", 120_000, orchestratorTask("small task")},
		{"a human message", 463_550, ports.ChatUserMessage{Text: "from a person", Origin: domain.MessageOriginHuman}},
		{"another sender", 463_550, ports.ChatUserMessage{Text: "from a worker", SenderSessionID: "worker-2", Origin: domain.MessageOriginAutomation}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, conv := newPreTaskHarness(t, 300_000)
			reportContext(t, h, conv.fakeConversation, tt.context)
			if _, err := h.svc.Send(context.Background(), testSession, tt.msg); err != nil {
				t.Fatalf("Send: %v", err)
			}
			awaitProviderTexts(t, conv.fakeConversation, tt.msg.Text)
			if conv.compactCalls() != 0 {
				t.Fatalf("compactions = %d, want 0", conv.compactCalls())
			}
		})
	}
}

// A provider that cannot compact runs the task as it always did.
func TestPreTaskCompactionWithoutCompactionSupportDelivers(t *testing.T) {
	h := newHarness(t)
	h.ctrl.SetPreTaskCompactionPolicy(orchestratorTasksOver(300_000))
	reportContext(t, h, h.conv, 463_550)
	if _, err := h.svc.Send(context.Background(), testSession, orchestratorTask("next task")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	awaitProviderTexts(t, h.conv, "next task")
}

// The service's rule applies only to an orchestrator handing a worker a task,
// at the worker project's threshold.
func TestPreTaskCompactionPolicyAppliesToOrchestratorTasksForWorkers(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	const worker = domain.SessionID("p1-2")
	now := time.Now().UTC()
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: worker, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	setCompaction := func(cfg domain.WorkerCompactionConfig) {
		t.Helper()
		project, found, err := st.GetProject(ctx, string(testProject))
		if err != nil || !found {
			t.Fatalf("read project: found=%v, err=%v", found, err)
		}
		project.Config.WorkerCompaction = cfg
		if err := st.UpsertProject(ctx, project); err != nil {
			t.Fatalf("update project: %v", err)
		}
	}
	svc := chatsvc.New(chatsvc.Options{Sessions: st, Projects: st})

	toWorker := svc.PreTaskCompactionPolicyFor(worker)
	if threshold, ok := toWorker(ctx, testSession); !ok || threshold != domain.DefaultWorkerCompactionThreshold {
		t.Fatalf("orchestrator -> worker = (%d, %v), want (%d, true)", threshold, ok, domain.DefaultWorkerCompactionThreshold)
	}
	if _, ok := toWorker(ctx, worker); ok {
		t.Fatal("a worker's own message triggered compact-before-task")
	}
	if _, ok := toWorker(ctx, "unknown-session"); ok {
		t.Fatal("an unknown sender triggered compact-before-task")
	}
	if _, ok := svc.PreTaskCompactionPolicyFor(testSession)(ctx, testSession); ok {
		t.Fatal("an orchestrator target triggered compact-before-task")
	}

	setCompaction(domain.WorkerCompactionConfig{ThresholdTokens: 200_000})
	if threshold, ok := toWorker(ctx, testSession); !ok || threshold != 200_000 {
		t.Fatalf("project threshold = (%d, %v), want (200000, true)", threshold, ok)
	}
	setCompaction(domain.WorkerCompactionConfig{Disabled: true})
	if _, ok := toWorker(ctx, testSession); ok {
		t.Fatal("compact-before-task ran with the project setting off")
	}

	if chatsvc.New(chatsvc.Options{Sessions: st}).PreTaskCompactionPolicyFor(worker) != nil {
		t.Fatal("a service without a project reader built a policy")
	}
}

// awaitSentCount waits until the provider has received n messages.
func awaitSentCount(t *testing.T, conv *fakeConversation, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := conv.sentTexts(); len(got) == n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider messages = %v, want %d", conv.sentTexts(), n)
	return nil
}

// A compaction the provider accepts but never starts, a hung /compact or a lost
// event, must not hold the task forever: nobody is watching an automatic one.
// After the deadline the task runs on the uncompacted context, and a late
// settlement does not deliver it twice.
func TestPreTaskCompactionDeadlineDeliversTheTask(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	h.ctrl.SetPreTaskCompactionDeadline(50 * time.Millisecond)
	ctx := context.Background()
	reportContext(t, h, conv.fakeConversation, 463_550)

	if _, err := h.svc.Send(ctx, testSession, orchestratorTask("next task")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1", conv.compactCalls())
	}
	awaitProviderTexts(t, conv.fakeConversation, "next task")

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventCompacted, ProviderTurnID: "compact-turn", Summary: "late"})
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	time.Sleep(100 * time.Millisecond)
	if got := conv.sentTexts(); len(got) != 1 {
		t.Fatalf("late settlement re-delivered the task: %v", got)
	}

	// The worker is not left busy: the task's turn ends and the next send goes out.
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send after: %v", err)
	}
	awaitProviderTexts(t, conv.fakeConversation, "next task", "after")
}

// A compaction whose turn started is a visible, stoppable turn. The deadline
// leaves it alone, so its settlement still releases the task even when the
// compaction fails.
func TestPreTaskCompactionDeadlineLeavesAStartedCompaction(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	h.ctrl.SetPreTaskCompactionDeadline(50 * time.Millisecond)
	ctx := context.Background()
	reportContext(t, h, conv.fakeConversation, 463_550)

	if _, err := h.svc.Send(ctx, testSession, orchestratorTask("next task")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "compact-turn"})
	time.Sleep(200 * time.Millisecond)
	if got := conv.sentTexts(); len(got) != 0 {
		t.Fatalf("task dispatched into a running compaction: %v", got)
	}

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn",
		TurnState: domain.TurnStateFailed, Err: errors.New("compaction failed"),
	})
	awaitProviderTexts(t, conv.fakeConversation, "next task")
}

// A deadline armed for a compaction that settled in time does nothing when it
// fires, even with another task queued behind the first.
func TestPreTaskCompactionDeadlineAfterSettlementIsInert(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	h.ctrl.SetPreTaskCompactionDeadline(100 * time.Millisecond)
	ctx := context.Background()
	reportContext(t, h, conv.fakeConversation, 463_550)

	if _, err := h.svc.Send(ctx, testSession, orchestratorTask("next task")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	awaitProviderTexts(t, conv.fakeConversation, "next task")
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send queued: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if got := conv.sentTexts(); len(got) != 1 {
		t.Fatalf("stale deadline dispatched into a running turn: %v", got)
	}
}

// The drain sends consecutive non-human turns as one. A CI or review notice
// queued ahead of an orchestrator's task must not hide the task from the rule.
func TestPreTaskCompactionChecksTheWholeBatch(t *testing.T) {
	h, conv := newPreTaskHarness(t, 300_000)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "current work", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	awaitProviderTexts(t, conv.fakeConversation, "current work")
	reportContext(t, h, conv.fakeConversation, 463_550)

	notice := ports.ChatUserMessage{Text: "CI failed on your PR", Origin: domain.MessageOriginAutomation}
	for _, msg := range []ports.ChatUserMessage{notice, orchestratorTask("next task")} {
		if turn, err := h.svc.Send(ctx, testSession, msg); err != nil || turn.State != domain.TurnStateQueued {
			t.Fatalf("Send %q while busy = %+v, %v", msg.Text, turn, err)
		}
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	deadline := time.Now().Add(5 * time.Second)
	for conv.compactCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want 1 for a batch holding an orchestrator task", conv.compactCalls())
	}
	if got := conv.sentTexts(); len(got) != 1 {
		t.Fatalf("batch dispatched before its compaction settled: %v", got)
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
	awaitSentCount(t, conv.fakeConversation, 2)
	if conv.compactCalls() != 1 {
		t.Fatalf("compactions = %d, want exactly 1", conv.compactCalls())
	}
}
