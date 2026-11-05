package domain

import (
	"errors"
	"testing"
	"time"
)

// bindingStates is the full closed set, so the transition table test cannot silently
// skip a state that was added later.
var bindingStates = []WorkflowBindingState{
	WorkflowBindingStateUnbound,
	WorkflowBindingStateIntentPersisted,
	WorkflowBindingStateSpawnRequested,
	WorkflowBindingStateSessionProvisioning,
	WorkflowBindingStateBoundReady,
	WorkflowBindingStateRunning,
	WorkflowBindingStateSpawnUnknown,
	WorkflowBindingStateReconcileRequired,
	WorkflowBindingStateFailedConfirmed,
	WorkflowBindingStateStopRequested,
	WorkflowBindingStateStopConfirmed,
	WorkflowBindingStateStopUnknown,
	WorkflowBindingStateRecoveryUncertain,
}

func TestWorkflowBindingStateValid(t *testing.T) {
	t.Parallel()
	for _, state := range bindingStates {
		if !state.Valid() {
			t.Fatalf("%q must be a valid binding state", state)
		}
	}
	for _, state := range []WorkflowBindingState{"", "spawned", "done", "RUNNING"} {
		if WorkflowBindingState(state).Valid() {
			t.Fatalf("%q must not be a valid binding state", state)
		}
	}
}

// TestWorkflowBindingTerminalStatesAreEvidenceNotTombstones pins that only the two
// proven-outcome states are terminal, and that a terminal transition still says
// nothing about deleting the session or worktree it names.
func TestWorkflowBindingTerminalStatesAreEvidenceNotTombstones(t *testing.T) {
	t.Parallel()
	terminal := map[WorkflowBindingState]bool{
		WorkflowBindingStateFailedConfirmed: true,
		WorkflowBindingStateStopConfirmed:   true,
	}
	for _, state := range bindingStates {
		if got := state.Terminal(); got != terminal[state] {
			t.Fatalf("%q Terminal() = %v, want %v", state, got, terminal[state])
		}
	}
}

// TestWorkflowBindingSessionCoherence pins the two predicates that must agree with the
// migration's CHECK constraints.
func TestWorkflowBindingSessionCoherence(t *testing.T) {
	t.Parallel()
	for _, state := range bindingStates {
		hasSession, preSession := state.HasSession(), state.PreSession()
		if hasSession && preSession {
			t.Fatalf("%q is both session-bearing and pre-session", state)
		}
	}
	// Spot-check the states that matter for the cross-table write.
	if !WorkflowBindingStateBoundReady.HasSession() {
		t.Fatal("bound_ready must carry a session")
	}
	if !WorkflowBindingStateRunning.HasSession() {
		t.Fatal("running must carry a session")
	}
	if !WorkflowBindingStateStopUnknown.HasSession() {
		t.Fatal("stop_unknown must carry the session it could not stop")
	}
	if !WorkflowBindingStateIntentPersisted.PreSession() {
		t.Fatal("intent_persisted must not carry a session")
	}
	if !WorkflowBindingStateSpawnRequested.PreSession() {
		t.Fatal("spawn_requested must not carry a session")
	}
	// spawn_unknown and reconcile_required deliberately belong to neither set: the
	// session may or may not exist, and claiming either would be a lie.
	if WorkflowBindingStateSpawnUnknown.PreSession() || WorkflowBindingStateSpawnUnknown.HasSession() {
		t.Fatal("spawn_unknown must be allowed to exist with or without a session")
	}
}

// TestWorkflowBindingTransitionRejectsIllegalEdges is the required test that an
// illegal transition is a typed error. It names the specific dangers rather than
// asserting only the graph shape.
func TestWorkflowBindingTransitionRejectsIllegalEdges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from WorkflowBindingState
		to   WorkflowBindingState
		why  string
	}{
		{
			"intent cannot skip to running", WorkflowBindingStateIntentPersisted,
			WorkflowBindingStateRunning, "running requires a bound, ready session",
		},
		{
			"spawn unknown cannot respawn", WorkflowBindingStateSpawnUnknown,
			WorkflowBindingStateSpawnRequested, "an unknown outcome must never be retried blindly",
		},
		{
			// Reconciliation may find a session that is still provisioning, so this edge
			// is legal (see TestWorkflowBindingLegalEdges). What must stay illegal is
			// RE-SPAWNING, which spawn_requested represents.
			"spawn unknown cannot respawn via requested", WorkflowBindingStateSpawnUnknown,
			WorkflowBindingStateSpawnRequested, "a lost answer must never be retried",
		},
		{
			"bound ready cannot go back to intent", WorkflowBindingStateBoundReady,
			WorkflowBindingStateIntentPersisted, "the session fact cannot be un-recorded",
		},
		{
			"stop confirmed is terminal", WorkflowBindingStateStopConfirmed,
			WorkflowBindingStateRunning, "a confirmed stop cannot resume",
		},
		{
			"failed confirmed is terminal", WorkflowBindingStateFailedConfirmed,
			WorkflowBindingStateBoundReady, "a proven failure cannot be undone by a transition",
		},
		{
			"running cannot bind again", WorkflowBindingStateRunning,
			WorkflowBindingStateBoundReady, "re-binding would re-point a live session",
		},
		{
			"stop requested cannot resume", WorkflowBindingStateStopRequested,
			WorkflowBindingStateRunning, "a stop in flight must settle first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.from.CanTransitionTo(tt.to) {
				t.Fatalf("%q → %q must be illegal: %s", tt.from, tt.to, tt.why)
			}
			if err := tt.from.ValidateTransition(tt.to); !errors.Is(err, ErrInvalidWorkflowBindingTransition) {
				t.Fatalf("ValidateTransition(%q → %q) = %v, want ErrInvalidWorkflowBindingTransition",
					tt.from, tt.to, err)
			}
		})
	}
}

// TestWorkflowBindingLegalEdges pins every edge the recovery paths depend on, so a
// future tightening cannot accidentally remove the only way out of an uncertain state.
func TestWorkflowBindingLegalEdges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from WorkflowBindingState
		to   WorkflowBindingState
	}{
		{"intent to spawn requested", WorkflowBindingStateIntentPersisted, WorkflowBindingStateSpawnRequested},
		{"intent straight to bound", WorkflowBindingStateIntentPersisted, WorkflowBindingStateBoundReady},
		{"spawn requested to provisioning", WorkflowBindingStateSpawnRequested, WorkflowBindingStateSessionProvisioning},
		{"spawn requested to bound", WorkflowBindingStateSpawnRequested, WorkflowBindingStateBoundReady},
		{"spawn requested to unknown", WorkflowBindingStateSpawnRequested, WorkflowBindingStateSpawnUnknown},
		{"provisioning to bound", WorkflowBindingStateSessionProvisioning, WorkflowBindingStateBoundReady},
		{"provisioning to reconcile", WorkflowBindingStateSessionProvisioning, WorkflowBindingStateReconcileRequired},
		{"unknown to reconcile", WorkflowBindingStateSpawnUnknown, WorkflowBindingStateReconcileRequired},
		{"unknown resolved to bound", WorkflowBindingStateSpawnUnknown, WorkflowBindingStateBoundReady},
		{"unknown adopts a provisioning session", WorkflowBindingStateSpawnUnknown, WorkflowBindingStateSessionProvisioning},
		{"reconcile adopts a provisioning session", WorkflowBindingStateReconcileRequired, WorkflowBindingStateSessionProvisioning},
		{"intent straight to provisioning", WorkflowBindingStateIntentPersisted, WorkflowBindingStateSessionProvisioning},
		{"reconcile to bound", WorkflowBindingStateReconcileRequired, WorkflowBindingStateBoundReady},
		{"reconcile to uncertain", WorkflowBindingStateReconcileRequired, WorkflowBindingStateRecoveryUncertain},
		{"bound to running", WorkflowBindingStateBoundReady, WorkflowBindingStateRunning},
		{"bound to stop requested", WorkflowBindingStateBoundReady, WorkflowBindingStateStopRequested},
		{"running to stop requested", WorkflowBindingStateRunning, WorkflowBindingStateStopRequested},
		{"uncertain back to running", WorkflowBindingStateRecoveryUncertain, WorkflowBindingStateRunning},
		{"stop requested to confirmed", WorkflowBindingStateStopRequested, WorkflowBindingStateStopConfirmed},
		{"stop requested to unknown", WorkflowBindingStateStopRequested, WorkflowBindingStateStopUnknown},
		{"stop unknown to confirmed", WorkflowBindingStateStopUnknown, WorkflowBindingStateStopConfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !tt.from.CanTransitionTo(tt.to) {
				t.Fatalf("%q → %q must be legal", tt.from, tt.to)
			}
			if err := tt.from.ValidateTransition(tt.to); err != nil {
				t.Fatalf("ValidateTransition(%q → %q) = %v, want nil", tt.from, tt.to, err)
			}
		})
	}
}

// TestWorkflowBindingSelfTransitionRejected proves a same-state write is not silently
// accepted; callers must use the revision CAS to express a replay.
func TestWorkflowBindingSelfTransitionRejected(t *testing.T) {
	t.Parallel()
	for _, state := range bindingStates {
		if state.CanTransitionTo(state) {
			t.Fatalf("%q → %q must not be a legal transition", state, state)
		}
	}
}

// TestWorkflowBindingTransitionRejectsUnknownStates covers both directions of the enum
// check before the graph is consulted.
func TestWorkflowBindingTransitionRejectsUnknownStates(t *testing.T) {
	t.Parallel()
	if err := WorkflowBindingState("bogus").ValidateTransition(WorkflowBindingStateRunning); !errors.Is(err, ErrInvalidWorkflowBindingState) {
		t.Fatalf("unknown source err = %v, want ErrInvalidWorkflowBindingState", err)
	}
	if err := WorkflowBindingStateRunning.ValidateTransition("bogus"); !errors.Is(err, ErrInvalidWorkflowBindingState) {
		t.Fatalf("unknown target err = %v, want ErrInvalidWorkflowBindingState", err)
	}
}

// TestWorkflowBindingTransitionGraphIsReachable proves every non-initial state can be
// reached and every state except the terminal pair can leave somewhere. A state that
// can be entered but never left would be a trap of the kind M1's UNKNOWN was.
func TestWorkflowBindingTransitionGraphIsReachable(t *testing.T) {
	t.Parallel()
	reached := map[WorkflowBindingState]bool{WorkflowBindingStateUnbound: true}
	changed := true
	for changed {
		changed = false
		for _, from := range bindingStates {
			if !reached[from] {
				continue
			}
			for _, to := range bindingStates {
				if from.CanTransitionTo(to) && !reached[to] {
					reached[to] = true
					changed = true
				}
			}
		}
	}
	for _, state := range bindingStates {
		if !reached[state] {
			t.Fatalf("%q is unreachable from unbound", state)
		}
		if state.Terminal() {
			continue
		}
		exits := 0
		for _, to := range bindingStates {
			if state.CanTransitionTo(to) {
				exits++
			}
		}
		if exits == 0 {
			t.Fatalf("%q is a trap: reachable but with no legal exit", state)
		}
	}
}

func TestWorkflowRepoBindingStateValid(t *testing.T) {
	t.Parallel()
	for _, state := range []WorkflowRepoBindingState{
		WorkflowRepoBindingStatePending, WorkflowRepoBindingStateVerified,
		WorkflowRepoBindingStateMismatch, WorkflowRepoBindingStateMissing,
		WorkflowRepoBindingStateUnknown,
	} {
		if !state.Valid() {
			t.Fatalf("%q must be valid", state)
		}
	}
	if WorkflowRepoBindingState("drifted").Valid() {
		t.Fatal("an unknown repo binding state must be invalid")
	}
}

func TestWorkflowAttemptBindingValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	valid := WorkflowAttemptBinding{
		AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
		SpawnRequestID: "spawn-1",
		SpawnHash:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		State:          WorkflowBindingStateIntentPersisted,
		Revision:       1, CreatedAt: now, UpdatedAt: now,
	}
	session := SessionID("p1-1")
	boundAt := now

	tests := []struct {
		name   string
		mutate func(*WorkflowAttemptBinding)
		want   error
	}{
		{"valid pre-session", func(*WorkflowAttemptBinding) {}, nil},
		{"valid bound", func(b *WorkflowAttemptBinding) {
			b.State = WorkflowBindingStateBoundReady
			b.SessionID = &session
			b.BoundAt = &boundAt
		}, nil},
		{"missing attempt", func(b *WorkflowAttemptBinding) { b.AttemptID = "" }, ErrInvalidWorkflowBinding},
		{"missing spawn request", func(b *WorkflowAttemptBinding) { b.SpawnRequestID = " " }, ErrInvalidWorkflowBinding},
		{"short hash", func(b *WorkflowAttemptBinding) { b.SpawnHash = "deadbeef" }, ErrInvalidWorkflowBinding},
		{"zero revision", func(b *WorkflowAttemptBinding) { b.Revision = 0 }, ErrInvalidWorkflowBinding},
		{"updated before created", func(b *WorkflowAttemptBinding) { b.UpdatedAt = now.Add(-time.Minute) }, ErrInvalidWorkflowBinding},
		{"bad state", func(b *WorkflowAttemptBinding) { b.State = "spawning" }, ErrInvalidWorkflowBindingState},
		{"bound without session", func(b *WorkflowAttemptBinding) {
			b.State = WorkflowBindingStateBoundReady
		}, ErrInvalidWorkflowBindingSession},
		{"bound without bound_at", func(b *WorkflowAttemptBinding) {
			b.State = WorkflowBindingStateBoundReady
			b.SessionID = &session
		}, ErrInvalidWorkflowBindingSession},
		{"pre-session claiming a session", func(b *WorkflowAttemptBinding) {
			b.State = WorkflowBindingStateIntentPersisted
			b.SessionID = &session
			b.BoundAt = &boundAt
		}, ErrInvalidWorkflowBindingSession},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			tt.mutate(&candidate)
			err := candidate.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestWorkflowAttemptRepoBindingValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	valid := WorkflowAttemptRepoBinding{
		AttemptID: "attempt-1", WorkflowID: "workflow-1", RepoKey: "backend",
		ProjectID: "project-1", ExpectedBaseSHA: "sha", State: WorkflowRepoBindingStatePending,
		Revision: 1, UpdatedAt: now,
	}
	tests := []struct {
		name   string
		mutate func(*WorkflowAttemptRepoBinding)
		want   error
	}{
		{"valid", func(*WorkflowAttemptRepoBinding) {}, nil},
		{"missing repo key", func(r *WorkflowAttemptRepoBinding) { r.RepoKey = "" }, ErrInvalidWorkflowRepoBinding},
		{"missing project", func(r *WorkflowAttemptRepoBinding) { r.ProjectID = "" }, ErrInvalidWorkflowRepoBinding},
		{"zero revision", func(r *WorkflowAttemptRepoBinding) { r.Revision = 0 }, ErrInvalidWorkflowRepoBinding},
		{"bad state", func(r *WorkflowAttemptRepoBinding) { r.State = "drifted" }, ErrInvalidWorkflowRepoBindingState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			tt.mutate(&candidate)
			err := candidate.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestWorkflowBindingIntentPersistedCanRecordALostAnswer pins the edge the M2-04
// coordinator needs: it persists the intent and spawns inline, so a lost spawn response
// must be recordable from intent_persisted. Without this edge the only honest outcome
// (spawn_unknown) would be unreachable and the caller would have to report a failure it
// cannot prove.
func TestWorkflowBindingIntentPersistedCanRecordALostAnswer(t *testing.T) {
	t.Parallel()
	for _, next := range []WorkflowBindingState{
		WorkflowBindingStateSpawnUnknown,
		WorkflowBindingStateReconcileRequired,
		WorkflowBindingStateFailedConfirmed,
		WorkflowBindingStateSpawnRequested,
		WorkflowBindingStateBoundReady,
	} {
		if err := WorkflowBindingStateIntentPersisted.ValidateTransition(next); err != nil {
			t.Fatalf("intent_persisted → %q = %v, want nil", next, err)
		}
	}
}
