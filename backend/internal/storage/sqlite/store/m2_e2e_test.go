package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// M2-08 offline end-to-end.
//
// Everything here runs against the real SQLite store, the real coordinators, and the FAKE
// execution gateway. No provider is started, no process is exec'd, no network call is made,
// and no user data directory is touched. That is the whole point of this file: it is the
// evidence that the M2 stack works end to end while remaining provably offline.

const e2eSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func e2eNow() time.Time { return time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC) }

// e2eHarness bundles one harness's coordinators over the shared store and its own gateway.
type e2eHarness struct {
	harness       domain.AgentHarness
	workflowID    domain.WorkflowID
	taskID        domain.WorkflowTaskID
	attempt       domain.WorkflowAttempt
	gateway       *execution.FakeGateway
	binder        *execution.Coordinator
	interventions *intervention.Coordinator
}

// newE2EHarness wires one harness. steerProven controls what the provider handshake reports
// about steering — that is the ONLY difference between a harness that may steer and one that
// may not, which is what makes the denial evidence-driven rather than name-driven.
//
// The probe re-stamps the snapshot with the session and generation actually being asked about,
// mirroring a real provider handshake, which can only ever describe the session it just ran in.
func newE2EHarness(
	t *testing.T,
	s *sqlite.Store,
	harness domain.AgentHarness,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	projectID string,
	steerProven bool,
	spawnReadiness ports.SessionReadiness,
) *e2eHarness {
	t.Helper()
	attempt := seedClaimedHarnessAttempt(t, s, workflowID, taskID, projectID, harness, e2eNow())

	gateway := execution.NewFakeGateway().
		WithClock(e2eNow).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID)).
		SpawnReadiness(spawnReadiness).
		SpawnGeneration("gen-" + string(harness)).
		SpawnWorktrees([]ports.RepoWorktreeObservation{{
			RepoKey: "backend", ProjectID: domain.ProjectID(projectID), WorktreeRef: t.TempDir(),
			BaseSHA: "base-sha-1", HeadSHA: "live-head-sha-2",
		}})

	resolver := capability.NewResolver(capability.WithClock(e2eNow))

	// A bind needs handshake-grade evidence to proceed at all, so BOTH harnesses get a
	// successful handshake. The only difference is whether it advertised steer.
	resolver.RegisterProbeFunc(harness,
		func(_ context.Context, request ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
			snapshot := domain.NewHarnessCapabilitySnapshot(
				harness, domain.CapabilitySourceProviderHandshake, e2eNow(), capability.DefaultTTL,
			)
			// The evidence is scoped to the exact session and generation that asked. Without
			// this scope the gate would answer from a harness-wide observation, which M2 forbids.
			snapshot.ObservedSessionID = request.SessionID
			snapshot.Generation = request.Generation
			snapshot.AuthState = domain.AgentAuthenticationAuthorized
			snapshot.Readiness = domain.CapabilityReady
			snapshot.Streaming = domain.CapabilitySupported
			snapshot.Queue = domain.CapabilitySupported
			snapshot.Interrupt = domain.CapabilitySupported
			snapshot.Resume = domain.CapabilityResumeWithHistory
			snapshot.Approval = domain.CapabilityApprovalEnforced
			snapshot.ApplyAt = domain.CapabilityApplyAtTurn
			snapshot.Usage = domain.CapabilityUsageExact
			if steerProven {
				snapshot.Steer = domain.CapabilitySupported
			} else {
				// The handshake succeeded but never advertised steering, so Steer stays
				// unknown. Unknown must refuse the control while still offering Queue.
				snapshot.Steer = domain.CapabilityStateUnknown
				snapshot.ReasonCode = "handshake_did_not_advertise_steer"
			}
			return snapshot, nil
		})

	return &e2eHarness{
		harness: harness, workflowID: workflowID, taskID: taskID, attempt: attempt,
		gateway: gateway,
		binder: execution.New(execution.Deps{
			ExecutionFence: executionTestFence(t, s),
			Store:          s, Gateway: gateway, Resolver: resolver, Clock: e2eNow,
		}),
		interventions: intervention.New(intervention.Deps{
			Store: s, Bindings: s, Gateway: gateway, Resolver: resolver, Clock: e2eNow,
		}),
	}
}

// TestM2OfflineEndToEndTwoHarnesses is the M2-08 headline test.
//
// It drives codex and claude-code through capability resolve → bind intent → fake spawn → bind
// → intervention → message → task request, CONCURRENTLY, and asserts that each ends at its own
// distinct binding and that nothing crosses between them.
func TestM2OfflineEndToEndTwoHarnesses(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Two separate workflows, so cross-workflow fencing is exercised as well as cross-session.
	projectID := "m2-e2e-project"
	codexWorkflow := domain.WorkflowID("workflow-e2e-codex")
	claudeWorkflow := domain.WorkflowID("workflow-e2e-claude")
	// seedClaimedAttempt (inside newE2EHarness) creates each workflow, its repository, and a
	// claimed attempt, so nothing is pre-created here.
	_ = projectID

	// codex: steer PROVEN at handshake grade, session READY.
	codex := newE2EHarness(t, s, domain.HarnessCodex, codexWorkflow, taskIDFor(codexWorkflow, "a"), projectID,
		true, ports.SessionReadinessReady)
	// claude-code: NO capability evidence at all, so steer must be denied and queue offered.
	claude := newE2EHarness(t, s, domain.HarnessClaudeCode, claudeWorkflow, taskIDFor(claudeWorkflow, "a"),
		projectID, false, ports.SessionReadinessReady)

	var wg sync.WaitGroup
	type harnessResult struct {
		outcome execution.BindOutcome
		err     error
	}
	results := make(map[domain.AgentHarness]harnessResult, 2)
	var mu sync.Mutex

	run := func(h *e2eHarness, repos []domain.WorkflowAttemptRepoBinding) {
		defer wg.Done()
		outcome, err := h.binder.BindAttempt(ctx, ports.SpawnRequest{
			WorkflowID: h.workflowID, TaskID: h.taskID, AttemptID: h.attempt.ID,
			ProjectID: domain.ProjectID(projectID), Harness: h.harness,
			Mode: domain.SessionModeChat, Prompt: "implement " + string(h.harness),
		}, repos)
		mu.Lock()
		results[h.harness] = harnessResult{outcome: outcome, err: err}
		mu.Unlock()
	}

	repos := func(h *e2eHarness) []domain.WorkflowAttemptRepoBinding {
		return []domain.WorkflowAttemptRepoBinding{{
			RepoKey: "backend", ProjectID: domain.ProjectID(projectID),
			ExpectedBaseSHA: "base-sha-1", State: domain.WorkflowRepoBindingStatePending,
		}}
	}

	wg.Add(2)
	go run(codex, repos(codex))
	go run(claude, repos(claude))
	wg.Wait()

	// --- each harness bound independently -----------------------------------------------
	for _, h := range []*e2eHarness{codex, claude} {
		result, ok := results[h.harness]
		if !ok {
			t.Fatalf("%s produced no result", h.harness)
		}
		if result.err != nil {
			t.Fatalf("%s BindAttempt: %v", h.harness, result.err)
		}
		if !result.outcome.Spawned {
			t.Fatalf("%s did not spawn", h.harness)
		}
		if result.outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
			t.Fatalf("%s state = %q, want bound_ready", h.harness, result.outcome.Binding.State)
		}
		if result.outcome.Binding.SessionID == nil {
			t.Fatalf("%s bound no session", h.harness)
		}
		if result.outcome.Binding.WorkflowID != h.workflowID {
			t.Fatalf("%s bound workflow %q, want %q", h.harness,
				result.outcome.Binding.WorkflowID, h.workflowID)
		}
		if result.outcome.Binding.TaskID != h.taskID {
			t.Fatalf("%s bound task %q, want %q", h.harness, result.outcome.Binding.TaskID, h.taskID)
		}
		if got := result.outcome.Binding.ControllerGeneration; got != "gen-"+string(h.harness) {
			t.Fatalf("%s generation = %q, want its own", h.harness, got)
		}
	}

	// --- distinct sessions, distinct bindings -------------------------------------------
	codexBinding, found, err := s.GetWorkflowAttemptBinding(ctx, codex.attempt.ID)
	if err != nil || !found {
		t.Fatalf("codex binding: found=%v err=%v", found, err)
	}
	claudeBinding, found, err := s.GetWorkflowAttemptBinding(ctx, claude.attempt.ID)
	if err != nil || !found {
		t.Fatalf("claude binding: found=%v err=%v", found, err)
	}
	if codexBinding.SessionID == nil || claudeBinding.SessionID == nil {
		t.Fatal("a binding has no session")
	}
	if *codexBinding.SessionID == *claudeBinding.SessionID {
		t.Fatalf("both harnesses share session %q; the bindings leaked into each other",
			*codexBinding.SessionID)
	}
	if codexBinding.ControllerGeneration == claudeBinding.ControllerGeneration {
		t.Fatal("both bindings share a controller generation")
	}
	// The remaining cross-field proof: no field of one binding describes the other.
	if codexBinding.WorkflowID == claudeBinding.WorkflowID {
		t.Fatal("both bindings name the same workflow; the two workflows were not separated")
	}
	if codexBinding.TaskID == claudeBinding.TaskID {
		t.Fatal("both bindings name the same task")
	}
	if codexBinding.SpawnRequestID == claudeBinding.SpawnRequestID {
		t.Fatal("both bindings share a spawn request id; one spawn would have served both")
	}
	// Each binding must point at the workflow that owns it, not merely at "a" workflow.
	if codexBinding.WorkflowID != codex.workflowID {
		t.Fatalf("codex binding workflow = %q, want %q", codexBinding.WorkflowID, codex.workflowID)
	}
	if claudeBinding.WorkflowID != claude.workflowID {
		t.Fatalf("claude binding workflow = %q, want %q", claudeBinding.WorkflowID, claude.workflowID)
	}

	// --- capability is per-session evidence, not a harness-name table -------------------
	//
	// The decisive experiment: ask the SAME harness name about TWO different sessions and get
	// two different answers. If capability were a harness-name table, codex would have to
	// answer the same way for both. Registering a probe that reports steer=supported for the
	// codex session and steer=unknown for the claude-code session proves the gate reads
	// per-session evidence.
	{
		mixed := capability.NewResolver(capability.WithClock(e2eNow))
		mixed.RegisterProbeFunc(domain.HarnessCodex,
			func(_ context.Context, request ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
				snapshot := domain.NewHarnessCapabilitySnapshot(
					domain.HarnessCodex, domain.CapabilitySourceProviderHandshake, e2eNow(),
					capability.DefaultTTL,
				)
				snapshot.ObservedSessionID = request.SessionID
				snapshot.Generation = request.Generation
				snapshot.AuthState = domain.AgentAuthenticationAuthorized
				snapshot.Readiness = domain.CapabilityReady
				snapshot.Streaming = domain.CapabilitySupported
				// Only codex's own session proved steer; any other session did not.
				if request.SessionID == *codexBinding.SessionID {
					snapshot.Steer = domain.CapabilitySupported
				} else {
					snapshot.Steer = domain.CapabilityStateUnknown
				}
				return snapshot, nil
			})

		asked := func(sessionID domain.SessionID, generation string) capability.Decision {
			return mixed.Gate(ctx, ports.CapabilityProbeRequest{
				Harness: domain.HarnessCodex, SessionID: sessionID, Generation: generation,
			}, domain.CapabilityFeatureSteer)
		}

		ownAnswer := asked(*codexBinding.SessionID, codexBinding.ControllerGeneration)
		otherAnswer := asked(*claudeBinding.SessionID, claudeBinding.ControllerGeneration)
		if !ownAnswer.Allowed {
			t.Fatalf("the same harness was refused on its own proven session: %+v", ownAnswer)
		}
		if otherAnswer.Allowed {
			t.Fatalf("the same harness was granted on another session's evidence: %+v", otherAnswer)
		}
		if ownAnswer.State == otherAnswer.State {
			t.Fatalf("both sessions got state %q, so the answer did not depend on the session",
				ownAnswer.State)
		}
		t.Logf("same harness, different sessions: proven=%v (%s) vs unproven=%v (%s)",
			ownAnswer.Allowed, ownAnswer.ReasonCode, otherAnswer.Allowed, otherAnswer.ReasonCode)
	}

	// codex is PROVEN and may steer.
	steerOutcome, err := codex.interventions.Steer(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), intervention.SteerRequest{
		WorkflowID: codex.workflowID, TaskID: codex.taskID, AttemptID: codex.attempt.ID,
		ClientMessageID: "e2e-steer-codex", Text: "use the streaming parser",
		TargetProviderTurnID: "prov-turn-codex", Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("codex Steer: %v", err)
	}
	if steerOutcome.QueueAvailable {
		t.Fatal("a proven steer offered the queue as a fallback")
	}
	if codex.gateway.SteerCalls() == 0 {
		t.Fatal("a proven steer never reached the gateway")
	}
	if codex.gateway.SendCount() != 0 {
		t.Fatalf("a proven steer was delivered as a send (%d sends)", codex.gateway.SendCount())
	}

	// claude-code is UNPROVEN and must be denied WITH queueAvailable.
	denied, err := claude.interventions.Steer(sqlitetest.ExecutionContext(t, ctx, s, claude.attempt.ID, e2eNow()), intervention.SteerRequest{
		WorkflowID: claude.workflowID, TaskID: claude.taskID, AttemptID: claude.attempt.ID,
		ClientMessageID: "e2e-steer-claude", Text: "use the streaming parser",
		TargetProviderTurnID: "prov-turn-claude", Harness: domain.HarnessClaudeCode,
	})
	if err != nil {
		t.Fatalf("claude Steer: %v", err)
	}
	if !denied.QueueAvailable {
		t.Fatal("a denied steer did not offer the queue")
	}
	if denied.ReasonCode == "" {
		t.Fatal("a denied steer carried no reason code")
	}
	if claude.gateway.SendCount() != 0 || claude.gateway.SteerCalls() != 0 {
		t.Fatalf("a denied steer reached the gateway: sends = %d steers = %d",
			claude.gateway.SendCount(), claude.gateway.SteerCalls())
	}
	if claude.gateway.InterruptCount() != 0 {
		t.Fatal("a denied steer fell back to interrupt-and-resend")
	}
	// The denied steer also recorded nothing: a refusal costs nothing.
	if _, found, _ := s.GetWorkflowInterventionByClientMessage(
		ctx, claude.workflowID, "e2e-steer-claude"); found {
		t.Fatal("a denied steer persisted an intervention")
	}

	// The denied harness can still QUEUE, which is the honest alternative.
	queued, err := claude.interventions.Queue(sqlitetest.ExecutionContext(t, ctx, s, claude.attempt.ID, e2eNow()), intervention.QueueRequest{
		WorkflowID: claude.workflowID, TaskID: claude.taskID, AttemptID: claude.attempt.ID,
		ClientMessageID: "e2e-queue-claude", Text: "do it on the next turn",
	})
	if err != nil {
		t.Fatalf("claude Queue: %v", err)
	}
	if queued.Intervention.State != domain.WorkflowInterventionStateUnknown &&
		queued.Intervention.State != domain.WorkflowInterventionStateQueued &&
		queued.Intervention.State != domain.WorkflowInterventionStateDelivered {
		t.Fatalf("queue state = %q, want a durable message state", queued.Intervention.State)
	}
	if claude.gateway.InterruptCount() != 0 {
		t.Fatal("queueing an intervention interrupted the session")
	}

	// --- one stop, on codex only -------------------------------------------------------
	codex.gateway.InterruptResult(ports.InterruptReceipt{Confirmed: true})
	stopOutcome, replan, err := codex.interventions.Stop(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), intervention.StopRequest{
		WorkflowID: codex.workflowID, TaskID: codex.taskID, AttemptID: codex.attempt.ID,
		ClientMessageID: "e2e-stop-codex",
	})
	if err != nil {
		t.Fatalf("codex Stop: %v", err)
	}
	if stopOutcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("codex stop state = %q, want stop_confirmed", stopOutcome.Intervention.State)
	}
	if replan == nil {
		t.Fatal("a confirmed stop emitted no replan request")
	}

	// --- collaboration between the two workflows is refused ----------------------------
	collab := collaboration.New(collaboration.Deps{Store: s, Bindings: s, Graph: s, Clock: e2eNow})

	// A message inside codex's own workflow succeeds.
	own, err := collab.CreateMessage(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), domain.CreateWorkflowMessageInput{
		WorkflowID: codex.workflowID, AttemptID: codex.attempt.ID,
		Kind: domain.WorkflowMessageKindProgress, Body: "halfway through the parser",
		ClientMessageID: "e2e-msg-codex",
	})
	if err != nil {
		t.Fatalf("codex message: %v", err)
	}
	if !own.Created {
		t.Fatal("the first message was reported as a replay")
	}
	if string(own.Message.FromTaskID) != string(codex.taskID) {
		t.Fatalf("message from_task = %q, want codex's task (sender is derived, never supplied)",
			own.Message.FromTaskID)
	}
	if own.Message.SenderSessionID == nil || *own.Message.SenderSessionID != *codexBinding.SessionID {
		t.Fatal("the message's sender session is not codex's bound session")
	}

	// codex's attempt speaking into claude's workflow is refused BEFORE any write.
	beforeCount := countMessages(t, s, claude.workflowID)
	if _, err := collab.CreateMessage(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), domain.CreateWorkflowMessageInput{
		WorkflowID: claude.workflowID, AttemptID: codex.attempt.ID,
		Kind: domain.WorkflowMessageKindQuestion, Body: "cross-workflow",
		ClientMessageID: "e2e-msg-cross",
	}); !errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
		t.Fatalf("cross-workflow message err = %v, want ErrCollaborationCrossWorkflow", err)
	}
	if after := countMessages(t, s, claude.workflowID); after != beforeCount {
		t.Fatalf("the refused message wrote %d rows", after-beforeCount)
	}

	// --- a task request stays pending --------------------------------------------------
	request, err := collab.CreateTaskRequest(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), collaboration.CreateTaskRequestInput{
		WorkflowID: codex.workflowID, AttemptID: codex.attempt.ID,
		RequestID: "e2e-request-1", RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		Payload: json.RawMessage(`{"title":"add parser tests"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}
	if request.Request.State != domain.WorkflowTaskRequestStatePending {
		t.Fatalf("request state = %q, want pending", request.Request.State)
	}

	// --- global call accounting ---------------------------------------------------------
	// Two spawns for two harnesses.
	totalSpawns := codex.gateway.SpawnCalls() + claude.gateway.SpawnCalls()
	if totalSpawns != 2 {
		t.Fatalf("total spawn calls = %d, want exactly 2", totalSpawns)
	}
	// Exactly one interrupt, on codex, and none on claude.
	totalInterrupts := codex.gateway.InterruptCount() + claude.gateway.InterruptCount()
	if totalInterrupts != 1 {
		t.Fatalf("total interrupt calls = %d, want exactly 1", totalInterrupts)
	}
	if claude.gateway.InterruptCount() != 0 {
		t.Fatalf("claude saw %d interrupts, want 0", claude.gateway.InterruptCount())
	}
	// No operation retried: replays of the same client message ids stay free.
	if _, err := collab.CreateMessage(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), domain.CreateWorkflowMessageInput{
		WorkflowID: codex.workflowID, AttemptID: codex.attempt.ID,
		Kind: domain.WorkflowMessageKindProgress, Body: "halfway through the parser",
		ClientMessageID: "e2e-msg-codex",
	}); err != nil {
		t.Fatalf("message replay: %v", err)
	}
	if _, _, err := codex.interventions.Stop(sqlitetest.ExecutionContext(t, ctx, s, codex.attempt.ID, e2eNow()), intervention.StopRequest{
		WorkflowID: codex.workflowID, TaskID: codex.taskID, AttemptID: codex.attempt.ID,
		ClientMessageID: "e2e-stop-codex",
	}); err != nil {
		t.Fatalf("stop replay: %v", err)
	}
	if total := codex.gateway.InterruptCount() + claude.gateway.InterruptCount(); total != 1 {
		t.Fatalf("a replay produced an extra interrupt: total = %d", total)
	}
	if total := codex.gateway.SpawnCalls() + claude.gateway.SpawnCalls(); total != 2 {
		t.Fatalf("a replay produced an extra spawn: total = %d", total)
	}
}

// taskIDFor builds a workflow-scoped task id, because workflow_tasks.id is globally unique.
func taskIDFor(workflowID domain.WorkflowID, suffix string) domain.WorkflowTaskID {
	return domain.WorkflowTaskID("task-" + string(workflowID) + "-" + suffix)
}

func countMessages(t *testing.T, s *sqlite.Store, workflowID domain.WorkflowID) int {
	t.Helper()
	rows, err := s.ListWorkflowMessages(context.Background(), workflowID, 1000, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	return len(rows)
}
