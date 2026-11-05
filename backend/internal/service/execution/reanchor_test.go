package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Controller replacement re-anchoring, against the real SQLite store.
//
// These tests exist because controller_generation is otherwise written exactly once, by the
// bind CAS that refuses to touch a row which already has a session. A chat controller that
// stops and starts again over the same session rotates its generation, and a binding that
// kept the old one fences the LIVE controller out of every intervention path forever: the
// fence fails closed, so no stale work reaches the replacement, but the replacement becomes
// unaddressable.
//
// The property that matters is asymmetric, and every test here is a facet of it: the
// recorded generation must be repaired, and the OLD generation must still be refused.

var reanchorNow = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func reanchorClock() func() time.Time { return func() time.Time { return reanchorNow } }

// reanchorProject is the project every fixture here seeds.
const reanchorProject = "reanchor-project"

func reanchorHash(b byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = b
	}
	return string(out)
}

// newReanchorStore opens a migrated store and seeds a workflow with one repository, an active
// state, and a claimed attempt — everything a real bind needs.
func newReanchorStore(t *testing.T) (*sqlite.Store, domain.WorkflowAttempt) {
	t.Helper()
	ctx := context.Background()
	s := sqlitetest.MustOpenAt(t, t.TempDir())
	admission := sqlitetest.NewAdmissionFixture(t, s, reanchorClock())
	if err := s.UpsertProject(ctx, domain.ProjectRecord{
		ID: reanchorProject, Path: "/tmp/" + reanchorProject,
		RegisteredAt: reanchorNow.Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	workflowID := domain.WorkflowID("workflow-reanchor")
	taskID := domain.WorkflowTaskID("task-reanchor")
	if _, _, err := s.CreateWorkflow(ctx, domain.Workflow{
		ID: workflowID, Title: "Reanchor", Goal: "prove the generation moves only forward",
		State: domain.WorkflowStateDraft, Revision: 1,
		CreatedAt: reanchorNow, UpdatedAt: reanchorNow,
	}, []domain.WorkflowRepository{{
		WorkflowID: workflowID, RepoKey: "backend", ProjectID: reanchorProject,
		BaseSHA: "base-sha-1", TargetRef: "main",
	}}, []domain.WorkflowTask{{
		ID: taskID, WorkflowID: workflowID, Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Implement reanchor", State: domain.WorkflowTaskStatePending,
		MaxAttempts: 2, Revision: 1, CreatedAt: reanchorNow, UpdatedAt: reanchorNow,
	}}, nil, nil, "reanchor-create", reanchorHash('a')); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "reanchor-activate",
		reanchorHash('b'), reanchorNow.Add(time.Second)); err != nil {
		t.Fatalf("ActivateWorkflow: %v", err)
	}
	task, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowTask: found=%v err=%v", found, err)
	}
	attempt, created, err := admission.Claim(ctx, domain.WorkflowAttempt{
		ID: "attempt-reanchor", WorkflowID: workflowID, TaskID: taskID, WorkerID: "worker-1",
		LeaseToken: "lease-reanchor", LeaseUntil: new(reanchorNow.Add(time.Hour)),
		State:     domain.WorkflowAttemptStateClaimed,
		CreatedAt: reanchorNow.Add(2 * time.Second), UpdatedAt: reanchorNow.Add(2 * time.Second),
	}, task.Revision, "reanchor-claim", reanchorHash('c'))
	if err != nil || !created {
		t.Fatalf("ClaimWorkflowTask: created=%v err=%v", created, err)
	}
	leader, err := s.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RenewSchedulerLeader(ctx, leader, reanchorNow, reanchorNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return s, attempt
}

// seededSession creates the session row a binding names, because
// workflow_attempt_bindings.session_id is a real foreign key.
func seededSession(t *testing.T, s *sqlite.Store) domain.SessionID {
	t.Helper()
	now := reanchorNow.Truncate(time.Second)
	created, err := s.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: reanchorProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return created.ID
}

// reanchorFixture binds one attempt at generation gen-1 and returns the durable binding.
func reanchorFixture(t *testing.T) (*sqlite.Store, domain.SessionID, domain.WorkflowAttemptBinding) {
	t.Helper()
	s, attempt := newReanchorStore(t)
	sessionID := seededSession(t, s)

	intent := domain.WorkflowAttemptBinding{
		AttemptID: attempt.ID, WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID,
		SpawnRequestID: "spawn-reanchor", SpawnHash: reanchorHash('d'),
	}
	binding, created, err := s.CreateWorkflowAttemptBinding(reanchorPermit(t, s, attempt.ID), intent, nil, reanchorNow)
	if err != nil || !created {
		t.Fatalf("CreateWorkflowAttemptBinding: created=%v err=%v", created, err)
	}
	bound, applied, err := s.BindWorkflowAttemptSession(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, "gen-1",
		domain.WorkflowBindingStateBoundReady, binding.Revision, reanchorNow,
	)
	if err != nil || !applied {
		t.Fatalf("BindWorkflowAttemptSession: applied=%v err=%v", applied, err)
	}
	if bound.ControllerGeneration != "gen-1" || bound.SessionID == nil {
		t.Fatalf("fixture bind = %+v, want gen-1 on a session", bound)
	}
	return s, sessionID, bound
}

// TestReanchorMovesTheBindingToTheNewGeneration proves the repair itself: same session, a
// rotated generation, one revision — and that nothing writes twice for one rotation.
func TestReanchorMovesTheBindingToTheNewGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, sessionID, binding := reanchorFixture(t)
	startRevision := binding.Revision

	reanchored, applied, err := s.ReanchorWorkflowAttemptGeneration(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, "gen-2", binding.Revision,
		binding.State, reanchorNow.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("ReanchorWorkflowAttemptGeneration: %v", err)
	}
	if !applied {
		t.Fatal("the re-anchor reported applied=false for a matching revision")
	}
	if reanchored.ControllerGeneration != "gen-2" {
		t.Fatalf("generation = %q, want gen-2", reanchored.ControllerGeneration)
	}
	if reanchored.Revision != startRevision+1 {
		t.Fatalf("revision = %d, want %d", reanchored.Revision, startRevision+1)
	}
	if reanchored.SessionID == nil || *reanchored.SessionID != sessionID {
		t.Fatalf("session = %v, want the unchanged %s", reanchored.SessionID, sessionID)
	}
	if reanchored.State != binding.State {
		t.Fatalf("state = %q, want the unchanged %q", reanchored.State, binding.State)
	}

	// The durable row, not just the return value.
	durable, found, err := s.GetWorkflowAttemptBinding(ctx, binding.AttemptID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowAttemptBinding: found=%v err=%v", found, err)
	}
	if durable.ControllerGeneration != "gen-2" || durable.Revision != startRevision+1 {
		t.Fatalf("durable binding = %+v, want gen-2 at revision %d", durable, startRevision+1)
	}

	// Idempotent: the coordinator only reaches this call when the observed generation differs
	// (pinned separately), and a second write of the same value is harmless because it carries
	// the value, not a delta. What must NOT happen is a second revision for one rotation, and
	// that is the predicate's job — asserted here against the row the predicate would be given.
	if reanchorable(durable, "gen-2") {
		t.Fatal("a binding already at the observed generation must not be re-anchored again")
	}
	if !reanchorable(durable, "gen-3") {
		t.Fatal("a rotated generation must be re-anchorable")
	}
}

// reanchorable restates the coordinator predicate from the outside, so the store test can ask
// the same "is there anything to repair" question the coordinator asks.
func reanchorable(binding domain.WorkflowAttemptBinding, generation string) bool {
	return binding.SessionID != nil && binding.ControllerGeneration != generation
}

// TestReanchorRefusesADifferentSession proves a generation repair can never become a rebind.
func TestReanchorRefusesADifferentSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, sessionID, binding := reanchorFixture(t)
	other := seededSession(t, s)
	if other == sessionID {
		t.Fatal("fixture problem: the two sessions are identical")
	}

	_, applied, err := s.ReanchorWorkflowAttemptGeneration(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, other, "gen-2", binding.Revision, binding.State, reanchorNow,
	)
	if err == nil {
		t.Fatal("a re-anchor naming another session reported no error")
	}
	if applied {
		t.Fatal("a re-anchor naming another session reported applied=true")
	}
	if !errors.Is(err, domain.ErrWorkflowBindingSessionTaken) {
		t.Fatalf("err = %v, want ErrWorkflowBindingSessionTaken", err)
	}

	durable, found, err := s.GetWorkflowAttemptBinding(ctx, binding.AttemptID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowAttemptBinding: found=%v err=%v", found, err)
	}
	if durable.ControllerGeneration != "gen-1" {
		t.Fatalf("generation = %q, want the unchanged gen-1", durable.ControllerGeneration)
	}
	if durable.SessionID == nil || *durable.SessionID != sessionID {
		t.Fatalf("session = %v, want the unchanged %s", durable.SessionID, sessionID)
	}
	if durable.Revision != binding.Revision {
		t.Fatalf("revision = %d, want the unchanged %d", durable.Revision, binding.Revision)
	}
}

// TestReanchorRefusesAnEmptyOrPlaceholderGeneration proves no observation that is not an owner
// ever overwrites a real generation.
//
// The emptiness half is the store's own refusal. The placeholder half is the coordinator's: the
// store cannot know this package's "pending:" marker without duplicating it, so the predicate
// is where that rule lives — and both halves are asserted so neither is assumed.
func TestReanchorRefusesAnEmptyOrPlaceholderGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, generation := range []string{"", "   "} {
		t.Run("store refuses "+generation, func(t *testing.T) {
			t.Parallel()
			s, sessionID, binding := reanchorFixture(t)
			_, applied, err := s.ReanchorWorkflowAttemptGeneration(
				reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, generation, binding.Revision,
				binding.State, reanchorNow,
			)
			if err == nil || applied {
				t.Fatalf("generation %q: applied=%v err=%v, want a refusal", generation, applied, err)
			}
			if !errors.Is(err, domain.ErrInvalidWorkflowBindingSession) {
				t.Fatalf("err = %v, want ErrInvalidWorkflowBindingSession", err)
			}
			durable, _, err := s.GetWorkflowAttemptBinding(ctx, binding.AttemptID)
			if err != nil {
				t.Fatalf("GetWorkflowAttemptBinding: %v", err)
			}
			if durable.ControllerGeneration != "gen-1" || durable.Revision != binding.Revision {
				t.Fatalf("binding = %+v, want the unchanged gen-1 at revision %d",
					durable, binding.Revision)
			}
		})
	}

	t.Run("coordinator refuses the placeholder", func(t *testing.T) {
		t.Parallel()
		s, attempt := newReanchorStore(t)
		sessionID := seededSession(t, s)
		gateway := reanchorGateway(sessionID)
		binding := reanchorBound(t, s, gateway, attempt)
		coordinator := newReanchorCoordinator(t, s, gateway)

		gateway.SetSessionObservation(ports.SessionObservation{
			SessionID: sessionID, ControllerGeneration: "pending:ready",
			Readiness: ports.SessionReadinessReady, ObservedAt: reanchorNow,
			Worktrees: reanchorWorktrees(),
		})
		outcome, err := coordinator.Resume(ctx, binding.AttemptID)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if outcome.Binding.ControllerGeneration != "gen-1" {
			t.Fatalf("generation = %q, want the unchanged gen-1", outcome.Binding.ControllerGeneration)
		}
		if outcome.Binding.Revision != binding.Revision {
			t.Fatalf("revision = %d, want the unchanged %d",
				outcome.Binding.Revision, binding.Revision)
		}
		if outcome.ReasonCode == "generation_reanchored" {
			t.Fatal("a placeholder generation was recorded as an owner")
		}
	})
}

// TestReanchorCASIsRevisionFenced proves a stale expectation cannot apply, in revision and in
// state: the CAS is the only thing between two concurrent observations and a lost update.
func TestReanchorCASIsRevisionFenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, sessionID, binding := reanchorFixture(t)

	_, firstApplied, err := s.ReanchorWorkflowAttemptGeneration(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, "gen-2", binding.Revision,
		binding.State, reanchorNow,
	)
	if err != nil || !firstApplied {
		t.Fatalf("first re-anchor: applied=%v err=%v", firstApplied, err)
	}

	// A caller that read the binding BEFORE the first write still holds the old revision.
	_, staleApplied, err := s.ReanchorWorkflowAttemptGeneration(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, "gen-3", binding.Revision,
		binding.State, reanchorNow.Add(time.Minute),
	)
	if err == nil || staleApplied {
		t.Fatalf("stale-revision re-anchor: applied=%v err=%v, want a refusal", staleApplied, err)
	}
	if !errors.Is(err, domain.ErrWorkflowBindingRevisionConflict) {
		t.Fatalf("err = %v, want ErrWorkflowBindingRevisionConflict", err)
	}

	durable, _, err := s.GetWorkflowAttemptBinding(ctx, binding.AttemptID)
	if err != nil {
		t.Fatalf("GetWorkflowAttemptBinding: %v", err)
	}
	if durable.ControllerGeneration != "gen-2" {
		t.Fatalf("generation = %q, want gen-2: a stale CAS must not write", durable.ControllerGeneration)
	}

	// The state is part of the fence too: a caller whose row moved on must not write a
	// generation under the assumption it is still bound_ready.
	moved, movedApplied, err := s.TransitionWorkflowAttemptBinding(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, domain.WorkflowBindingStateStopRequested, "", durable.Revision,
		reanchorNow.Add(2*time.Minute),
	)
	if err != nil || !movedApplied {
		t.Fatalf("transition to stop_requested: applied=%v err=%v", movedApplied, err)
	}
	_, wrongState, err := s.ReanchorWorkflowAttemptGeneration(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, "gen-3", moved.Revision,
		domain.WorkflowBindingStateBoundReady, reanchorNow.Add(3*time.Minute),
	)
	if err == nil || wrongState {
		t.Fatalf("wrong-state re-anchor: applied=%v err=%v, want a refusal", wrongState, err)
	}
}

// TestReanchorKeepsOldGenerationFenced is the property the fix is judged by: after the repair
// the OLD generation is still refused, and only the NEW one is accepted.
func TestReanchorKeepsOldGenerationFenced(t *testing.T) {
	t.Parallel()
	s, sessionID, binding := reanchorFixture(t)

	reanchored, applied, err := s.ReanchorWorkflowAttemptGeneration(
		reanchorPermit(t, s, binding.AttemptID), binding.AttemptID, sessionID, "gen-2", binding.Revision,
		binding.State, reanchorNow,
	)
	if err != nil || !applied {
		t.Fatalf("re-anchor: applied=%v err=%v", applied, err)
	}

	live := ports.SessionObservation{
		SessionID: sessionID, ControllerGeneration: "gen-2",
		Readiness: ports.SessionReadinessReady, ObservedAt: reanchorNow,
	}
	// The new generation is accepted…
	if err := live.ValidateFence(sessionID, reanchored.ControllerGeneration); err != nil {
		t.Fatalf("the re-anchored generation was refused: %v", err)
	}
	// …and the OLD one is not. This is why a re-anchor is not a "generation always passes"
	// hole: it repairs the recorded identity and leaves the fence semantics untouched.
	if err := live.ValidateFence(sessionID, "gen-1"); !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("the OLD generation was not refused: %v", err)
	}
	// A different session is still refused even at the right generation.
	if err := live.ValidateFence("other-session", reanchored.ControllerGeneration); !errors.Is(err, ports.ErrExecutionSessionMismatch) {
		t.Fatalf("a different session was not refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// coordinator level: the re-anchor branch, and the rotation end to end
// ---------------------------------------------------------------------------

// reanchorWorktrees 提供已核验的授权仓库证据，让重锚用例只改变 generation。
func reanchorWorktrees() []ports.RepoWorktreeObservation {
	return []ports.RepoWorktreeObservation{{
		RepoKey: "backend", ProjectID: reanchorProject, WorktreeRef: "/ws/backend",
		BaseSHA: "base-sha-1", HeadSHA: "head-sha-1",
	}}
}

// reanchorGateway builds a fake gateway whose spawn reports the seeded session as ready at
// gen-1. The materializer is how the fake names a session that really exists in the store,
// so the binding's foreign key is satisfied by the database rather than bypassed.
func reanchorGateway(sessionID domain.SessionID) *execution.FakeGateway {
	g := execution.NewFakeGateway().
		WithClock(reanchorClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1").
		SpawnWorktrees(reanchorWorktrees())
	g.WithSessionMaterializer(func(
		context.Context, domain.SessionID, ports.SpawnRequest,
	) (domain.SessionID, error) {
		return sessionID, nil
	})
	return g
}

// reanchorResolver proves every capability for the exact session generation that asks, which
// is what a real handshake can do and what the session-scoped gates require.
func reanchorResolver() *capability.Resolver {
	resolver := capability.NewResolver(capability.WithClock(reanchorClock()))
	resolver.RegisterProbeFunc(domain.HarnessCodex,
		func(_ context.Context, request ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
			snapshot := domain.NewHarnessCapabilitySnapshot(
				domain.HarnessCodex, domain.CapabilitySourceProviderHandshake,
				reanchorNow, capability.DefaultTTL,
			)
			snapshot.ObservedSessionID = request.SessionID
			snapshot.Generation = request.Generation
			snapshot.AuthState = domain.AgentAuthenticationAuthorized
			snapshot.Readiness = domain.CapabilityReady
			snapshot.Streaming = domain.CapabilitySupported
			snapshot.Tools = domain.CapabilitySupported
			snapshot.Interrupt = domain.CapabilitySupported
			snapshot.Steer = domain.CapabilitySupported
			snapshot.Queue = domain.CapabilitySupported
			snapshot.Resume = domain.CapabilityResumeWithHistory
			snapshot.Approval = domain.CapabilityApprovalEnforced
			snapshot.ApplyAt = domain.CapabilityApplyAtTurn
			snapshot.Usage = domain.CapabilityUsageExact
			return snapshot, nil
		})
	return resolver
}

func newReanchorCoordinator(t *testing.T, s *sqlite.Store, gateway ports.ExecutionGateway) *execution.Coordinator {
	t.Helper()
	return execution.New(execution.Deps{
		Store: s, Gateway: gateway, Resolver: reanchorResolver(), Clock: reanchorClock(),
		ExecutionFence: func(ctx context.Context) (domain.SchedulerFence, error) {
			leader, err := s.GetSchedulerLeader(ctx)
			return leader.Fence, err
		},
	})
}

// reanchorBound runs the real bind sequence and returns the bound row.
func reanchorBound(
	t *testing.T,
	s *sqlite.Store,
	gateway *execution.FakeGateway,
	attempt domain.WorkflowAttempt,
) domain.WorkflowAttemptBinding {
	t.Helper()
	coordinator := newReanchorCoordinator(t, s, gateway)
	outcome, err := coordinator.BindAttempt(context.Background(), ports.SpawnRequest{
		WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID, AttemptID: attempt.ID,
		ProjectID: reanchorProject, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, Prompt: "implement the task",
	}, []domain.WorkflowAttemptRepoBinding{{
		RepoKey: "backend", ProjectID: reanchorProject,
		ExpectedBaseSHA: "base-sha-1", State: domain.WorkflowRepoBindingStatePending,
	}})
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.SessionID == nil || outcome.Binding.ControllerGeneration != "gen-1" {
		t.Fatalf("bind = %+v, want a session bound at gen-1", outcome.Binding)
	}
	return outcome.Binding
}

// TestCoordinatorReanchorsOnAControllerReplacement proves the coordinator branch: an
// observation of the SAME session at a new generation repairs the binding without being read
// as a transition, and a repeat observation writes nothing.
func TestCoordinatorReanchorsOnAControllerReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, attempt := newReanchorStore(t)
	sessionID := seededSession(t, s)
	gateway := reanchorGateway(sessionID)
	binding := reanchorBound(t, s, gateway, attempt)
	coordinator := newReanchorCoordinator(t, s, gateway)

	// The controller is replaced over the same session: readiness is still ready, so the planned
	// target is bound_ready and the binding is already there — there is no forward edge.
	gateway.SetSessionObservation(ports.SessionObservation{
		SessionID: sessionID, ControllerGeneration: "gen-2",
		Readiness: ports.SessionReadinessReady, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, ObservedAt: reanchorNow,
		Worktrees: reanchorWorktrees(),
	})

	outcome, err := coordinator.Resume(ctx, binding.AttemptID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if outcome.ReasonCode != "generation_reanchored" {
		t.Fatalf("reason = %q, want generation_reanchored", outcome.ReasonCode)
	}
	if outcome.Frozen {
		t.Fatal("the re-anchor froze the binding")
	}
	if outcome.Binding.ControllerGeneration != "gen-2" {
		t.Fatalf("generation = %q, want gen-2", outcome.Binding.ControllerGeneration)
	}
	if outcome.Binding.Revision != binding.Revision+1 {
		t.Fatalf("revision = %d, want %d", outcome.Binding.Revision, binding.Revision+1)
	}
	if outcome.Binding.SessionID == nil || *outcome.Binding.SessionID != sessionID {
		t.Fatalf("session = %v, want the unchanged %s", outcome.Binding.SessionID, sessionID)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want the unchanged bound_ready", outcome.Binding.State)
	}
	if outcome.Spawned {
		t.Fatal("a re-anchor reported a spawn")
	}

	// Idempotent: a second observation at the same generation has nothing to repair, so the
	// revision does not move again.
	again, err := coordinator.Resume(ctx, binding.AttemptID)
	if err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	if again.ReasonCode != string(ports.SessionReadinessReady) {
		t.Fatalf("second reason = %q, want the readiness %q",
			again.ReasonCode, ports.SessionReadinessReady)
	}
	if again.Binding.Revision != outcome.Binding.Revision {
		t.Fatalf("revision = %d after a no-op observation, want %d",
			again.Binding.Revision, outcome.Binding.Revision)
	}
}

// TestCoordinatorReanchorRefusesAndFallsBack proves every refusal path leaves the binding
// exactly as it was, and never turns a lost CAS into a failed call.
func TestCoordinatorReanchorRefusesAndFallsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name        string
		observation func(sessionID domain.SessionID) ports.SessionObservation
	}{
		{
			name: "different session",
			observation: func(domain.SessionID) ports.SessionObservation {
				return ports.SessionObservation{
					SessionID: "someone-elses-session", ControllerGeneration: "gen-2",
					Readiness: ports.SessionReadinessReady, ObservedAt: reanchorNow,
				}
			},
		},
		{
			name: "empty generation",
			observation: func(sessionID domain.SessionID) ports.SessionObservation {
				return ports.SessionObservation{
					SessionID: sessionID, ControllerGeneration: "",
					Readiness: ports.SessionReadinessReady, ObservedAt: reanchorNow,
				}
			},
		},
		{
			name: "pending placeholder",
			observation: func(sessionID domain.SessionID) ports.SessionObservation {
				return ports.SessionObservation{
					SessionID: sessionID, ControllerGeneration: "pending:ready",
					Readiness: ports.SessionReadinessReady, ObservedAt: reanchorNow,
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, attempt := newReanchorStore(t)
			sessionID := seededSession(t, s)
			gateway := reanchorGateway(sessionID)
			binding := reanchorBound(t, s, gateway, attempt)
			coordinator := newReanchorCoordinator(t, s, gateway)

			observation := tt.observation(sessionID)
			observation.Worktrees = reanchorWorktrees()
			gateway.SetSessionObservation(observation)
			outcome, err := coordinator.Resume(ctx, binding.AttemptID)
			if err != nil {
				t.Fatalf("Resume returned an error for a refused re-anchor: %v", err)
			}
			if outcome.Binding.ControllerGeneration != "gen-1" {
				t.Fatalf("generation = %q, want the unchanged gen-1",
					outcome.Binding.ControllerGeneration)
			}
			if outcome.Binding.Revision != binding.Revision {
				t.Fatalf("revision = %d, want the unchanged %d",
					outcome.Binding.Revision, binding.Revision)
			}
			if outcome.ReasonCode == "generation_reanchored" {
				t.Fatal("a refused re-anchor reported success")
			}
		})
	}
}

// TestReanchorRepairsTheFenceEndToEnd is the end-to-end shape of the defect: after a real
// controller replacement and a re-anchor, Queue/Steer/Stop at the NEW generation succeed while
// the OLD one is still refused with the stable stale-generation error.
func TestReanchorRepairsTheFenceEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, attempt := newReanchorStore(t)
	sessionID := seededSession(t, s)
	gateway := reanchorGateway(sessionID)
	// The fake's default steer receipt is the one that proves the guidance joined the exact
	// turn and generation the caller named, which is what the intervention layer requires.
	gateway.InterruptResult(ports.InterruptReceipt{Confirmed: true})
	binding := reanchorBound(t, s, gateway, attempt)
	coordinator := newReanchorCoordinator(t, s, gateway)
	interventions := intervention.New(intervention.Deps{
		Store: s, Bindings: s, BindingWriter: coordinator, Gateway: gateway,
		Resolver: reanchorResolver(), Clock: reanchorClock(),
	})

	// Baseline: a generation nobody holds is refused before any replacement.
	_, _, err := interventions.Stop(ctx, intervention.StopRequest{
		WorkflowID: binding.WorkflowID, TaskID: binding.TaskID, AttemptID: binding.AttemptID,
		ClientMessageID: "reanchor-stop-nobody", ControllerGeneration: "gen-0",
	})
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want ErrExecutionStaleGeneration for a generation nobody holds", err)
	}

	// Replace the controller over the same session, then re-anchor through the real path.
	gateway.SetSessionObservation(ports.SessionObservation{
		SessionID: sessionID, ControllerGeneration: "gen-2",
		Readiness: ports.SessionReadinessReady, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, ObservedAt: reanchorNow,
		Worktrees: reanchorWorktrees(),
	})
	outcome, err := coordinator.Resume(ctx, binding.AttemptID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if outcome.ReasonCode != "generation_reanchored" {
		t.Fatalf("reason = %q, want generation_reanchored", outcome.ReasonCode)
	}

	// The OLD generation is still refused, through the same surface, and persisted nothing.
	_, _, err = interventions.Stop(ctx, intervention.StopRequest{
		WorkflowID: binding.WorkflowID, TaskID: binding.TaskID, AttemptID: binding.AttemptID,
		ClientMessageID: "reanchor-stop-stale", ControllerGeneration: "gen-1",
	})
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want the OLD generation still refused", err)
	}
	if _, found, err := s.GetWorkflowInterventionByClientMessage(
		ctx, binding.WorkflowID, "reanchor-stop-stale",
	); err != nil || found {
		t.Fatalf("a stale stop persisted a receipt: found=%v err=%v", found, err)
	}

	// The NEW generation is addressable in all three modes.
	queued, err := interventions.Queue(ctx, intervention.QueueRequest{
		WorkflowID: binding.WorkflowID, TaskID: binding.TaskID, AttemptID: binding.AttemptID,
		ClientMessageID: "reanchor-queue-new", Text: "carry on",
		ControllerGeneration: "gen-2",
	})
	if err != nil {
		t.Fatalf("Queue at the re-anchored generation: %v", err)
	}
	if queued.Intervention.ControllerGeneration != "gen-2" {
		t.Fatalf("queued generation = %q, want gen-2", queued.Intervention.ControllerGeneration)
	}
	if queued.Intervention.SessionID == nil || *queued.Intervention.SessionID != sessionID {
		t.Fatalf("queued session = %v, want %s", queued.Intervention.SessionID, sessionID)
	}

	steered, err := interventions.Steer(ctx, intervention.SteerRequest{
		WorkflowID: binding.WorkflowID, TaskID: binding.TaskID, AttemptID: binding.AttemptID,
		ClientMessageID: "reanchor-steer-new", Text: "use the helper",
		TargetProviderTurnID: "prov-turn-1", ControllerGeneration: "gen-2",
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("Steer at the re-anchored generation: %v", err)
	}
	if steered.ReasonCode != "applied" {
		t.Fatalf("steer reason = %q, want applied", steered.ReasonCode)
	}
	if got := gateway.LastSteer().ControllerGeneration; got != "gen-2" {
		t.Fatalf("gateway steer generation = %q, want gen-2", got)
	}

	stopped, _, err := interventions.Stop(ctx, intervention.StopRequest{
		WorkflowID: binding.WorkflowID, TaskID: binding.TaskID, AttemptID: binding.AttemptID,
		ClientMessageID: "reanchor-stop-new", ControllerGeneration: "gen-2",
	})
	if err != nil {
		t.Fatalf("Stop at the re-anchored generation: %v", err)
	}
	if stopped.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("stop state = %q, want stop_confirmed", stopped.Intervention.State)
	}
	if got := gateway.LastInterrupt().ControllerGeneration; got != "gen-2" {
		t.Fatalf("gateway interrupt generation = %q, want gen-2", got)
	}

	// Nothing above re-pointed or destroyed the session: a stop settles a binding.
	durable, found, err := s.GetWorkflowAttemptBinding(ctx, binding.AttemptID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowAttemptBinding: found=%v err=%v", found, err)
	}
	if durable.SessionID == nil || *durable.SessionID != sessionID {
		t.Fatalf("session = %v, want the unchanged %s", durable.SessionID, sessionID)
	}
}
