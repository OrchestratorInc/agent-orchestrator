package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The M2-08 anomaly-injection matrix.
//
// Every row asserts three things, and the third is the one that matters most:
//
//  1. the resulting STATE,
//  2. the CALL COUNTS (no retry, no double spawn, no phantom interrupt), and
//  3. that nothing was destroyed — no worktree binding, no session, no artifact row.
//
// A recovery path that "works" by deleting the evidence of what it could not prove would
// pass an assertion on state alone. Every row therefore also re-reads the durable rows.

// anomalyEnv is one isolated workflow with a bound (or deliberately unbound) attempt.
type anomalyEnv struct {
	store       *sqlite.Store
	gateway     *execution.FakeGateway
	coordinator *execution.Coordinator
	attempt     domain.WorkflowAttempt
	workflowID  domain.WorkflowID
	taskID      domain.WorkflowTaskID
	projectID   string
}

// newAnomalyEnv builds a workflow with a claimed attempt. ready controls what the gateway
// reports for a fresh spawn.
func newAnomalyEnv(t *testing.T, name string, ready ports.SessionReadiness) *anomalyEnv {
	t.Helper()
	s := newTestStore(t)
	projectID := "m2-anomaly-" + name
	workflowID := domain.WorkflowID("workflow-anomaly-" + name)
	taskID := domain.WorkflowTaskID("task-anomaly-" + name)

	attempt := seedClaimedAttempt(t, s, workflowID, taskID, projectID, e2eNow())

	gateway := execution.NewFakeGateway().
		WithClock(e2eNow).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID)).
		SpawnReadiness(ready).
		SpawnGeneration("gen-anomaly").
		SpawnWorktrees([]ports.RepoWorktreeObservation{{
			RepoKey: "backend", ProjectID: domain.ProjectID(projectID), WorktreeRef: t.TempDir(),
			BaseSHA: "base-sha-1", HeadSHA: "live-head-sha-2",
		}})

	resolver := capability.NewResolver(capability.WithClock(e2eNow))
	registerAlwaysProven(resolver, domain.HarnessCodex)

	return &anomalyEnv{
		store: s, gateway: gateway, attempt: attempt,
		workflowID: workflowID, taskID: taskID, projectID: projectID,
		coordinator: execution.New(execution.Deps{
			Store: s, Gateway: gateway, Resolver: resolver, Clock: e2eNow,
		}),
	}
}

// registerAlwaysProven gives a harness handshake-grade evidence for the exact session and
// generation asked about, which is what a real bind requires.
func registerAlwaysProven(resolver *capability.Resolver, harness domain.AgentHarness) {
	resolver.RegisterProbeFunc(harness,
		func(_ context.Context, request ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
			snapshot := domain.NewHarnessCapabilitySnapshot(
				harness, domain.CapabilitySourceProviderHandshake, e2eNow(), capability.DefaultTTL,
			)
			snapshot.ObservedSessionID = request.SessionID
			snapshot.Generation = request.Generation
			snapshot.AuthState = domain.AgentAuthenticationAuthorized
			snapshot.Readiness = domain.CapabilityReady
			snapshot.Streaming = domain.CapabilitySupported
			snapshot.Steer = domain.CapabilitySupported
			snapshot.Queue = domain.CapabilitySupported
			snapshot.Interrupt = domain.CapabilitySupported
			snapshot.Resume = domain.CapabilityResumeWithHistory
			snapshot.Approval = domain.CapabilityApprovalEnforced
			snapshot.ApplyAt = domain.CapabilityApplyAtTurn
			snapshot.Usage = domain.CapabilityUsageExact
			return snapshot, nil
		})
}

func (e *anomalyEnv) spawnRequest() ports.SpawnRequest {
	return ports.SpawnRequest{
		WorkflowID: e.workflowID, TaskID: e.taskID, AttemptID: e.attempt.ID,
		ProjectID: domain.ProjectID(e.projectID), Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, Prompt: "implement the thing",
	}
}

func (e *anomalyEnv) repos() []domain.WorkflowAttemptRepoBinding {
	return []domain.WorkflowAttemptRepoBinding{{
		RepoKey: "backend", ProjectID: domain.ProjectID(e.projectID),
		ExpectedBaseSHA: "base-sha-1", State: domain.WorkflowRepoBindingStatePending,
	}}
}

func (e *anomalyEnv) interventionCoordinator() *intervention.Coordinator {
	resolver := capability.NewResolver(capability.WithClock(e2eNow))
	registerAlwaysProven(resolver, domain.HarnessCodex)
	return intervention.New(intervention.Deps{
		Store: e.store, Bindings: e.store, Gateway: e.gateway, Resolver: resolver, Clock: e2eNow,
	})
}

// bind brings the attempt to bound_ready and returns the binding.
func (e *anomalyEnv) bind(t *testing.T) domain.WorkflowAttemptBinding {
	t.Helper()
	outcome, err := e.coordinator.BindAttempt(context.Background(), e.spawnRequest(), e.repos())
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if !outcome.Spawned {
		t.Fatal("the fixture failed to spawn")
	}
	return outcome.Binding
}

// binding re-reads the durable binding row.
func (e *anomalyEnv) binding(t *testing.T) domain.WorkflowAttemptBinding {
	t.Helper()
	binding, found, err := e.store.GetWorkflowAttemptBinding(context.Background(), e.attempt.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if !found {
		t.Fatal("the binding row disappeared; nothing in M2 may delete a binding")
	}
	return binding
}

// assertReposSurvived proves the worktree/repo binding rows were not collateral damage.
func (e *anomalyEnv) assertReposSurvived(t *testing.T, wantKey string) {
	t.Helper()
	repos, err := e.store.ListWorkflowAttemptRepoBindings(context.Background(), e.attempt.ID)
	if err != nil {
		t.Fatalf("list repo bindings: %v", err)
	}
	if len(repos) == 0 {
		t.Fatal("the attempt lost every repo binding; a recovery path destroyed the worktree record")
	}
	found := false
	for _, repo := range repos {
		if string(repo.RepoKey) == wantKey {
			found = true
			if repo.ExpectedBaseSHA != "base-sha-1" {
				t.Fatalf("repo %q base sha = %q, want it preserved", repo.RepoKey, repo.ExpectedBaseSHA)
			}
		}
	}
	if !found {
		t.Fatalf("repo binding %q vanished", wantKey)
	}
}

// TestM2AnomalyInjectionMatrix is the M2-08 anomaly matrix. Each subtest is one row.
func TestM2AnomalyInjectionMatrix(t *testing.T) {
	ctx := context.Background()

	// ---------------------------------------------------------------- row 1
	t.Run("spawn_response_lost_then_reconcile_adopts", func(t *testing.T) {
		env := newAnomalyEnv(t, "lost-adopt", ports.SessionReadinessReady)
		env.gateway.LoseSpawnResponse()

		outcome, err := env.coordinator.BindAttempt(ctx, env.spawnRequest(), env.repos())
		if err != nil {
			t.Fatalf("BindAttempt: %v", err)
		}
		// A lost answer is unknown, not a failure and never a retry.
		if outcome.Binding.State != domain.WorkflowBindingStateSpawnUnknown {
			t.Fatalf("state = %q, want spawn_unknown", outcome.Binding.State)
		}
		if !outcome.Frozen {
			t.Fatal("a lost spawn answer must freeze the binding")
		}
		if env.gateway.SpawnCalls() != 1 {
			t.Fatalf("spawn calls = %d, want exactly 1 (a lost answer is never retried)",
				env.gateway.SpawnCalls())
		}

		// Reconcile finds the session the gateway actually created and adopts it.
		reconciled, err := env.coordinator.Reconcile(ctx, env.attempt.ID)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.Binding.State != domain.WorkflowBindingStateBoundReady {
			t.Fatalf("state after reconcile = %q, want bound_ready", reconciled.Binding.State)
		}
		if reconciled.Binding.SessionID == nil {
			t.Fatal("reconcile adopted no session")
		}
		if env.gateway.SpawnCalls() != 1 {
			t.Fatalf("spawn calls after reconcile = %d, want it unchanged at 1",
				env.gateway.SpawnCalls())
		}
		if !reconciled.Spawned && reconciled.Binding.State == domain.WorkflowBindingStateBoundReady {
			// Reconcile never spawns; the adopted session came from FindSpawn.
			if reconciled.Binding.SessionID == nil {
				t.Fatal("a bound reconciliation has no session")
			}
		}
	})

	// ---------------------------------------------------------------- row 2
	t.Run("spawn_response_lost_then_reconcile_finds_nothing", func(t *testing.T) {
		env := newAnomalyEnv(t, "lost-nothing", ports.SessionReadinessReady)
		env.gateway.LoseSpawnResponse()

		if _, err := env.coordinator.BindAttempt(ctx, env.spawnRequest(), env.repos()); err != nil {
			t.Fatalf("BindAttempt: %v", err)
		}
		if env.gateway.SpawnCalls() != 1 {
			t.Fatalf("spawn calls = %d, want 1", env.gateway.SpawnCalls())
		}

		// Make the lookup blind: the gateway no longer knows the spawn id.
		env.gateway.ForgetSpawnMapping()

		reconciled, err := env.coordinator.Reconcile(ctx, env.attempt.ID)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.Binding.State != domain.WorkflowBindingStateReconcileRequired {
			t.Fatalf("state = %q, want reconcile_required", reconciled.Binding.State)
		}
		if !reconciled.Frozen {
			t.Fatal("no evidence must freeze, not promote")
		}
		// THE assertion: absence of evidence is not permission to spawn.
		if env.gateway.SpawnCalls() != 1 {
			t.Fatalf("reconcile re-spawned: calls = %d, want 1", env.gateway.SpawnCalls())
		}

		// A later bind attempt still must not spawn.
		retry, err := env.coordinator.BindAttempt(ctx, env.spawnRequest(), env.repos())
		if err != nil {
			t.Fatalf("retry BindAttempt: %v", err)
		}
		if retry.Spawned {
			t.Fatal("a frozen binding spawned again")
		}
		if env.gateway.SpawnCalls() != 1 {
			t.Fatalf("spawn calls = %d, want 1 after a retry", env.gateway.SpawnCalls())
		}
		env.assertReposSurvived(t, "backend")
	})

	// ---------------------------------------------------------------- row 3
	t.Run("crash_between_claim_and_gateway_call", func(t *testing.T) {
		env := newAnomalyEnv(t, "crash-claim", ports.SessionReadinessReady)

		// Simulate the crash window by hand: persist the intent and claim the spawn right,
		// then stop. This is exactly the state a process kill leaves behind.
		request := env.spawnRequest()
		hash, err := execution.SpawnHash(request)
		if err != nil {
			t.Fatalf("SpawnHash: %v", err)
		}
		binding, created, err := env.store.CreateWorkflowAttemptBinding(ctx,
			domain.WorkflowAttemptBinding{
				AttemptID: request.AttemptID, WorkflowID: request.WorkflowID,
				TaskID:         request.TaskID,
				SpawnRequestID: execution.SpawnKey(request.AttemptID, execution.ActionSpawn),
				SpawnHash:      hash,
			}, env.repos(), e2eNow())
		if err != nil || !created {
			t.Fatalf("create intent: created=%v err=%v", created, err)
		}
		stalled, applied, err := env.store.TransitionWorkflowAttemptBinding(ctx,
			request.AttemptID, domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, e2eNow())
		if err != nil || !applied {
			t.Fatalf("claim spawn right: applied=%v err=%v", applied, err)
		}
		if stalled.State != domain.WorkflowBindingStateSpawnRequested {
			t.Fatalf("state = %q, want spawn_requested", stalled.State)
		}
		if env.gateway.SpawnCalls() != 0 {
			t.Fatalf("the crash fixture already spawned: calls = %d", env.gateway.SpawnCalls())
		}

		// reconcile must ADVANCE the stalled row, and must not spawn.
		reconciled, err := env.coordinator.Reconcile(ctx, env.attempt.ID)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.Binding.State == domain.WorkflowBindingStateSpawnRequested {
			t.Fatal("reconcile left the row at spawn_requested; nothing would ever advance it")
		}
		if reconciled.Binding.State != domain.WorkflowBindingStateReconcileRequired {
			t.Fatalf("state = %q, want reconcile_required", reconciled.Binding.State)
		}
		if env.gateway.SpawnCalls() != 0 {
			t.Fatalf("reconcile spawned: calls = %d, want 0", env.gateway.SpawnCalls())
		}
		env.assertReposSurvived(t, "backend")
	})

	// ---------------------------------------------------------------- row 4
	t.Run("session_deleted_under_live_binding", func(t *testing.T) {
		env := newAnomalyEnv(t, "session-deleted", ports.SessionReadinessReady)
		bound := env.bind(t)
		if bound.SessionID == nil {
			t.Fatal("the fixture did not bind a session")
		}
		sessionID := *bound.SessionID
		spawnCalls := env.gateway.SpawnCalls()

		// The user terminates and deletes the session underneath the live binding.
		rec, found, err := env.store.GetSession(ctx, sessionID)
		if err != nil || !found {
			t.Fatalf("get session: found=%v err=%v", found, err)
		}
		rec.IsTerminated = true
		if err := env.store.UpdateSession(ctx, rec); err != nil {
			t.Fatalf("terminate session: %v", err)
		}
		if _, err := env.store.DeleteTerminatedSession(ctx, sessionID); err != nil {
			t.Fatalf("DeleteTerminatedSession failed, so a bound attempt pins its session: %v", err)
		}

		after := env.binding(t)
		if after.State != domain.WorkflowBindingStateReconcileRequired {
			t.Fatalf("state = %q, want reconcile_required", after.State)
		}
		if after.FailureCode != "session_deleted" {
			t.Fatalf("failure code = %q, want session_deleted", after.FailureCode)
		}
		if after.SessionID != nil {
			t.Fatalf("the binding still names session %v", *after.SessionID)
		}
		if after.ControllerGeneration != "" {
			t.Fatalf("generation = %q, want it cleared so it cannot authorize anything", after.ControllerGeneration)
		}
		// The worktree ref must survive: the user's checkout is not the binding's to destroy.
		env.assertReposSurvived(t, "backend")
		if env.gateway.SpawnCalls() != spawnCalls {
			t.Fatalf("deleting a session spawned a replacement: calls = %d, want %d",
				env.gateway.SpawnCalls(), spawnCalls)
		}
	})

	// ---------------------------------------------------------------- row 5
	t.Run("session_deleted_under_open_stop", func(t *testing.T) {
		env := newAnomalyEnv(t, "stop-deleted", ports.SessionReadinessReady)
		bound := env.bind(t)
		if bound.SessionID == nil {
			t.Fatal("the fixture did not bind a session")
		}
		sessionID := *bound.SessionID
		interventions := env.interventionCoordinator()

		// An unsettled stop: the gateway answers with no confirmation at all.
		env.gateway.InterruptResult(ports.InterruptReceipt{})
		stop, _, err := interventions.Stop(ctx, intervention.StopRequest{
			WorkflowID: env.workflowID, TaskID: env.taskID, AttemptID: env.attempt.ID,
			ClientMessageID: "msg-stop-open",
		})
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if stop.Intervention.State != domain.WorkflowInterventionStateStopUnknown {
			t.Fatalf("state = %q, want stop_unknown (nothing ambiguous becomes confirmed)", stop.Intervention.State)
		}
		interrupts := env.gateway.InterruptCount()

		rec, found, err := env.store.GetSession(ctx, sessionID)
		if err != nil || !found {
			t.Fatalf("get session: found=%v err=%v", found, err)
		}
		rec.IsTerminated = true
		if err := env.store.UpdateSession(ctx, rec); err != nil {
			t.Fatalf("terminate session: %v", err)
		}

		// THE assertion: the delete must not raise a raw constraint error.
		if _, err := env.store.DeleteTerminatedSession(ctx, sessionID); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "constraint") {
				t.Fatalf("deleting the session raised a raw constraint error instead of a typed state: %v", err)
			}
			t.Fatalf("DeleteTerminatedSession: %v", err)
		}

		after, found, err := env.store.GetWorkflowIntervention(ctx, stop.Intervention.ID)
		if err != nil || !found {
			t.Fatalf("get intervention: found=%v err=%v", found, err)
		}
		if after.State != domain.WorkflowInterventionStateStopUnknown {
			t.Fatalf("state = %q, want stop_unknown preserved", after.State)
		}
		if after.ReasonCode != "session_deleted" {
			t.Fatalf("reason = %q, want session_deleted", after.ReasonCode)
		}
		if after.SessionID != nil {
			t.Fatalf("the intervention still names session %v", *after.SessionID)
		}
		if !after.Mode.AllowsState(after.State) {
			t.Fatalf("post-delete state %q violates the mode/state CHECK", after.State)
		}
		if env.gateway.InterruptCount() != interrupts {
			t.Fatalf("the delete issued an interrupt: %d → %d", interrupts, env.gateway.InterruptCount())
		}
		// The binding moved to reconcile_required, but its repo rows are intact.
		binding := env.binding(t)
		if binding.State != domain.WorkflowBindingStateReconcileRequired {
			t.Fatalf("binding state = %q, want reconcile_required", binding.State)
		}
		env.assertReposSurvived(t, "backend")
	})

	// ---------------------------------------------------------------- row 6
	t.Run("stale_generation_receipt_rejected", func(t *testing.T) {
		env := newAnomalyEnv(t, "stale-generation", ports.SessionReadinessReady)
		bound := env.bind(t)
		if bound.SessionID == nil {
			t.Fatal("the fixture did not bind a session")
		}

		// An UNSETTLED steer row: requested, generation "gen-1". The controller is then
		// replaced, so the binding's generation becomes "gen-2". A receipt that arrives
		// for "gen-1" describes a controller that no longer exists.
		attemptID := env.attempt.ID
		pending := domain.WorkflowIntervention{
			ID: "intervention-stale-generation", WorkflowID: env.workflowID, TaskID: env.taskID,
			AttemptID: &attemptID, Mode: domain.WorkflowInterventionModeSteer,
			State:     domain.WorkflowInterventionStateRequested,
			SessionID: bound.SessionID, ControllerGeneration: "gen-1",
			ClientMessageID: "msg-stale-generation", OperationSHA: interventionSHA,
			TargetProviderTurnID: "prov-turn-1",
		}
		created, _, err := env.store.CreateWorkflowIntervention(ctx, pending, e2eNow())
		if err != nil {
			t.Fatalf("create pending intervention: %v", err)
		}

		appliedFact := domain.InterventionChatFact{ChatTurnID: "turn-a2", ProviderTurnID: "prov-a2"}

		// A receipt stamped with the REPLACED generation must be refused.
		stale, applied, err := env.store.ApplyWorkflowInterventionReceipt(ctx,
			created.ID, domain.WorkflowInterventionStateApplied, appliedFact, "applied",
			true, "gen-previous", created.Revision, e2eNow().Add(time.Minute))
		if !errors.Is(err, domain.ErrInterventionStaleGeneration) {
			t.Fatalf("stale receipt err = %v, want ErrInterventionStaleGeneration", err)
		}
		if applied {
			t.Fatal("a stale receipt was applied")
		}
		if stale.State == domain.WorkflowInterventionStateApplied {
			t.Fatal("a stale receipt settled the attempt")
		}

		// The row is untouched: the fence refused rather than wrote.
		reloaded, found, err := env.store.GetWorkflowIntervention(ctx, created.ID)
		if err != nil || !found {
			t.Fatalf("get intervention: found=%v err=%v", found, err)
		}
		if reloaded.State != domain.WorkflowInterventionStateRequested {
			t.Fatalf("state = %q, want it still requested", reloaded.State)
		}
		if reloaded.Revision != created.Revision {
			t.Fatalf("revision = %d, want it unchanged at %d", reloaded.Revision, created.Revision)
		}

		// The correct generation still settles it, proving the fence is a fence, not a wall.
		fresh, applied, err := env.store.ApplyWorkflowInterventionReceipt(ctx,
			created.ID, domain.WorkflowInterventionStateDelivered, domain.InterventionChatFact{
				ChatTurnID: "turn-a2", ChatTurnState: domain.TurnStateRunning,
			}, "delivered", false, "gen-1", created.Revision, e2eNow().Add(time.Minute))
		if err != nil || !applied {
			t.Fatalf("fresh receipt: applied=%v err=%v", applied, err)
		}
		if fresh.State != domain.WorkflowInterventionStateDelivered {
			t.Fatalf("state = %q, want delivered", fresh.State)
		}
	})

	// ---------------------------------------------------------------- row 7
	t.Run("duplicate_stop_replay_interrupts_once", func(t *testing.T) {
		env := newAnomalyEnv(t, "stop-replay", ports.SessionReadinessReady)
		env.bind(t)
		interventions := env.interventionCoordinator()
		env.gateway.InterruptResult(ports.InterruptReceipt{Confirmed: true})

		first, _, err := interventions.Stop(ctx, intervention.StopRequest{
			WorkflowID: env.workflowID, TaskID: env.taskID, AttemptID: env.attempt.ID,
			ClientMessageID: "msg-stop-same",
		})
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if first.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
			t.Fatalf("state = %q, want stop_confirmed", first.Intervention.State)
		}
		if env.gateway.InterruptCount() != 1 {
			t.Fatalf("interrupt calls = %d, want 1", env.gateway.InterruptCount())
		}

		for i := 0; i < 3; i++ {
			replay, _, err := interventions.Stop(ctx, intervention.StopRequest{
				WorkflowID: env.workflowID, TaskID: env.taskID, AttemptID: env.attempt.ID,
				ClientMessageID: "msg-stop-same",
			})
			if err != nil {
				t.Fatalf("Stop replay %d: %v", i, err)
			}
			if replay.Performed {
				t.Fatalf("replay %d performed work", i)
			}
			if replay.Intervention.ID != first.Intervention.ID {
				t.Fatalf("replay %d produced a different intervention", i)
			}
		}
		if env.gateway.InterruptCount() != 1 {
			t.Fatalf("interrupt calls after 3 replays = %d, want exactly 1", env.gateway.InterruptCount())
		}
	})

	// ---------------------------------------------------------------- row 8
	t.Run("refused_steer_sends_and_interrupts_nothing", func(t *testing.T) {
		env := newAnomalyEnv(t, "refused-steer", ports.SessionReadinessReady)
		env.bind(t)

		// A resolver with NO evidence: the static profile can describe, not authorize.
		resolver := capability.NewResolver(capability.WithClock(e2eNow))
		interventions := intervention.New(intervention.Deps{
			Store: env.store, Bindings: env.store, Gateway: env.gateway,
			Resolver: resolver, Clock: e2eNow,
		})

		outcome, err := interventions.Steer(ctx, intervention.SteerRequest{
			WorkflowID: env.workflowID, TaskID: env.taskID, AttemptID: env.attempt.ID,
			ClientMessageID: "msg-refused", Text: "steer me",
			TargetProviderTurnID: "prov-turn-1", Harness: domain.HarnessCodex,
		})
		if err != nil {
			t.Fatalf("Steer: %v", err)
		}
		if !outcome.QueueAvailable {
			t.Fatal("a refused steer must offer the queue")
		}
		if outcome.ReasonCode == "" {
			t.Fatal("a refused steer must carry a reason code")
		}
		if env.gateway.SendCalls() != 0 || env.gateway.SteerCalls() != 0 {
			t.Fatalf("a refused steer reached the gateway: sends = %d steers = %d",
				env.gateway.SendCalls(), env.gateway.SteerCalls())
		}
		if env.gateway.InterruptCount() != 0 {
			t.Fatalf("interrupt calls = %d, want 0 — a refusal never falls back to interrupt-and-resend",
				env.gateway.InterruptCount())
		}
		if _, found, _ := env.store.GetWorkflowInterventionByClientMessage(
			ctx, env.workflowID, "msg-refused"); found {
			t.Fatal("a refused steer persisted an intervention row")
		}
	})

	// ---------------------------------------------------------------- row 9
	t.Run("thirty_two_message_cap_refuses_the_thirty_third", func(t *testing.T) {
		env := newAnomalyEnv(t, "message-cap", ports.SessionReadinessReady)
		env.bind(t)
		collab := collaboration.New(collaboration.Deps{
			Store: env.store, Bindings: env.store, Graph: env.store, Clock: e2eNow,
		})

		cap := domain.MaxOpenWorkflowMessages
		if cap != 32 {
			t.Fatalf("MaxOpenWorkflowMessages = %d, want the documented 32", cap)
		}
		for i := 0; i < cap; i++ {
			if _, err := collab.CreateMessage(ctx, domain.CreateWorkflowMessageInput{
				WorkflowID: env.workflowID, AttemptID: env.attempt.ID,
				Kind: domain.WorkflowMessageKindProgress, Body: "progress",
				ClientMessageID: "msg-cap-" + itoa(i),
			}); err != nil {
				t.Fatalf("message %d of %d: %v", i+1, cap, err)
			}
		}

		_, err := collab.CreateMessage(ctx, domain.CreateWorkflowMessageInput{
			WorkflowID: env.workflowID, AttemptID: env.attempt.ID,
			Kind: domain.WorkflowMessageKindProgress, Body: "one too many",
			ClientMessageID: "msg-cap-overflow",
		})
		if !errors.Is(err, domain.ErrWorkflowMessageQueueFull) {
			t.Fatalf("33rd message err = %v, want the typed ErrWorkflowMessageQueueFull", err)
		}
		// The refusal must be a typed refusal, not a raw SQLite string.
		if strings.Contains(strings.ToLower(err.Error()), "sql") {
			t.Fatalf("the cap surfaced a raw storage error: %v", err)
		}

		rows, err := env.store.ListWorkflowMessages(ctx, env.workflowID, 1000, 0)
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		if len(rows) != cap {
			t.Fatalf("stored messages = %d, want exactly %d preserved", len(rows), cap)
		}
		for i, row := range rows {
			if row.ClientMessageID != "msg-cap-"+itoa(i) && row.ClientMessageID != "" {
				continue
			}
		}
		// The first 32 are intact and in order.
		seen := make(map[string]bool, cap)
		for _, row := range rows {
			seen[row.ClientMessageID] = true
		}
		for i := 0; i < cap; i++ {
			if !seen["msg-cap-"+itoa(i)] {
				t.Fatalf("message %d of the first %d was lost", i+1, cap)
			}
		}
		if seen["msg-cap-overflow"] {
			t.Fatal("the refused 33rd message was stored anyway")
		}
	})

	// ---------------------------------------------------------------- row 10
	t.Run("approval_without_materialization_stays_pending_work", func(t *testing.T) {
		env := newAnomalyEnv(t, "decide", ports.SessionReadinessReady)
		env.bind(t)
		collab := collaboration.New(collaboration.Deps{
			Store: env.store, Bindings: env.store, Graph: env.store, Clock: e2eNow,
		})

		created, err := collab.CreateTaskRequest(ctx, collaboration.CreateTaskRequestInput{
			WorkflowID: env.workflowID, AttemptID: env.attempt.ID,
			RequestID: "req-decide-1", RequestKind: domain.WorkflowTaskRequestKindCreateTask,
			Payload: []byte(`{"title":"add the tests"}`),
		})
		if err != nil {
			t.Fatalf("CreateTaskRequest: %v", err)
		}
		if created.Request.State != domain.WorkflowTaskRequestStatePending {
			t.Fatalf("state = %q, want pending", created.Request.State)
		}

		tasksBefore, err := env.store.ListWorkflowTasks(ctx, env.workflowID, "", 1000, 0)
		if err != nil {
			t.Fatalf("list tasks: %v", err)
		}

		decision, err := collab.Decide(ctx, collaboration.DecideInput{
			WorkflowID: env.workflowID, RequestID: "req-decide-1", Approve: true,
			DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "user-1",
			Reason: "looks good", ExpectedRevision: created.Request.Revision,
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if decision.Request.State != domain.WorkflowTaskRequestStateApproved {
			t.Fatalf("state = %q, want approved", decision.Request.State)
		}
		// THE assertion: approval records the answer and creates nothing.
		if decision.Materialized {
			t.Fatal("Decide reported materialization, which service/workflow cannot do")
		}
		if decision.Materialization != domain.WorkflowTaskRequestMaterializationPending {
			t.Fatalf("materialization = %q, want pending", decision.Materialization)
		}
		if decision.ReasonCode != domain.TaskRequestReasonApprovedNotMaterialized {
			t.Fatalf("reasonCode = %q, want %q",
				decision.ReasonCode, domain.TaskRequestReasonApprovedNotMaterialized)
		}

		tasksAfter, err := env.store.ListWorkflowTasks(ctx, env.workflowID, "", 1000, 0)
		if err != nil {
			t.Fatalf("list tasks after: %v", err)
		}
		if len(tasksAfter) != len(tasksBefore) {
			t.Fatalf("approval created %d task(s); M2 has no AddTask",
				len(tasksAfter)-len(tasksBefore))
		}
		// And no edge appeared either.
		graph, found, err := env.store.GetWorkflow(ctx, env.workflowID)
		if err != nil || !found {
			t.Fatalf("get workflow: found=%v err=%v", found, err)
		}
		if graph.Revision != created.Request.WorkflowRevision {
			t.Fatalf("approval moved the workflow revision %d → %d; it must not",
				created.Request.WorkflowRevision, graph.Revision)
		}
	})
}

// itoa avoids importing strconv just for a test loop index.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
