package domain

import (
	"errors"
	"testing"
	"time"
)

var allInterventionStates = []WorkflowInterventionState{
	WorkflowInterventionStateRequested,
	WorkflowInterventionStateQueued,
	WorkflowInterventionStateDelivered,
	WorkflowInterventionStateApplied,
	WorkflowInterventionStateRejected,
	WorkflowInterventionStateCancelled,
	WorkflowInterventionStateUnknown,
	WorkflowInterventionStateStopRequested,
	WorkflowInterventionStateStopConfirmed,
	WorkflowInterventionStateStopUnknown,
}

func TestWorkflowInterventionModeAndStateValidity(t *testing.T) {
	t.Parallel()
	for _, mode := range []WorkflowInterventionMode{
		WorkflowInterventionModeSteer, WorkflowInterventionModeQueue, WorkflowInterventionModeStop,
	} {
		if !mode.Valid() {
			t.Fatalf("%q must be valid", mode)
		}
	}
	if WorkflowInterventionMode("pause").Valid() {
		t.Fatal("an unknown mode must be invalid")
	}
	for _, state := range allInterventionStates {
		if !state.Valid() {
			t.Fatalf("%q must be valid", state)
		}
	}
	if WorkflowInterventionState("sent").Valid() {
		t.Fatal("an unknown state must be invalid (the design's forbidden vocabulary must not sneak in)")
	}
}

// TestInterventionModeStateCompatibility is the required "a steer can never claim a stop
// state" proof, in both directions.
func TestInterventionModeStateCompatibility(t *testing.T) {
	t.Parallel()
	stopOnly := []WorkflowInterventionState{
		WorkflowInterventionStateStopRequested,
		WorkflowInterventionStateStopConfirmed,
		WorkflowInterventionStateStopUnknown,
	}
	for _, state := range stopOnly {
		if WorkflowInterventionModeSteer.AllowsState(state) {
			t.Fatalf("steer must not be allowed to occupy %q", state)
		}
		if WorkflowInterventionModeQueue.AllowsState(state) {
			t.Fatalf("queue must not be allowed to occupy %q", state)
		}
		if !WorkflowInterventionModeStop.AllowsState(state) {
			t.Fatalf("stop must be allowed to occupy %q", state)
		}
	}
	messageOnly := []WorkflowInterventionState{
		WorkflowInterventionStateQueued,
		WorkflowInterventionStateDelivered,
		WorkflowInterventionStateApplied,
		WorkflowInterventionStateCancelled,
	}
	for _, state := range messageOnly {
		if WorkflowInterventionModeStop.AllowsState(state) {
			t.Fatalf("stop must not be allowed to occupy %q", state)
		}
	}
	// Requested and rejected are the two states all three modes share.
	for _, mode := range []WorkflowInterventionMode{
		WorkflowInterventionModeSteer, WorkflowInterventionModeQueue, WorkflowInterventionModeStop,
	} {
		for _, state := range []WorkflowInterventionState{
			WorkflowInterventionStateRequested, WorkflowInterventionStateRejected,
		} {
			if !mode.AllowsState(state) {
				t.Fatalf("%q must be allowed to occupy %q", mode, state)
			}
		}
	}
}

func TestAllowedStatesForIsStable(t *testing.T) {
	t.Parallel()
	steer := AllowedStatesFor(WorkflowInterventionModeSteer)
	wantSteer := []WorkflowInterventionState{
		WorkflowInterventionStateRequested, WorkflowInterventionStateQueued,
		WorkflowInterventionStateDelivered, WorkflowInterventionStateApplied,
		WorkflowInterventionStateRejected, WorkflowInterventionStateCancelled,
		WorkflowInterventionStateUnknown,
	}
	if len(steer) != len(wantSteer) {
		t.Fatalf("steer states = %v, want %v", steer, wantSteer)
	}
	for i := range wantSteer {
		if steer[i] != wantSteer[i] {
			t.Fatalf("steer states = %v, want %v", steer, wantSteer)
		}
	}
	if got := len(AllowedStatesFor(WorkflowInterventionModeStop)); got != 5 {
		t.Fatalf("stop states = %d, want 5 (requested, rejected, stop_requested, stop_confirmed, stop_unknown)", got)
	}
}

func TestInterventionTerminalStates(t *testing.T) {
	t.Parallel()
	terminal := map[WorkflowInterventionState]bool{
		WorkflowInterventionStateApplied:       true,
		WorkflowInterventionStateRejected:      true,
		WorkflowInterventionStateCancelled:     true,
		WorkflowInterventionStateStopConfirmed: true,
	}
	for _, state := range allInterventionStates {
		if got := state.Terminal(); got != terminal[state] {
			t.Fatalf("%q Terminal() = %v, want %v", state, got, terminal[state])
		}
	}
}

// TestInterventionTransitionsRespectEvidence proves a receipt never walks backwards into a
// weaker claim.
func TestInterventionTransitionsRespectEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode WorkflowInterventionMode
		from WorkflowInterventionState
		to   WorkflowInterventionState
		ok   bool
	}{
		{"steer requested to queued", WorkflowInterventionModeSteer, WorkflowInterventionStateRequested, WorkflowInterventionStateQueued, true},
		{"steer requested to delivered", WorkflowInterventionModeSteer, WorkflowInterventionStateRequested, WorkflowInterventionStateDelivered, true},
		{"steer requested to applied", WorkflowInterventionModeSteer, WorkflowInterventionStateRequested, WorkflowInterventionStateApplied, true},
		{"steer queued to delivered", WorkflowInterventionModeSteer, WorkflowInterventionStateQueued, WorkflowInterventionStateDelivered, true},
		{"steer delivered to applied", WorkflowInterventionModeSteer, WorkflowInterventionStateDelivered, WorkflowInterventionStateApplied, true},
		{"steer applied to delivered", WorkflowInterventionModeSteer, WorkflowInterventionStateApplied, WorkflowInterventionStateDelivered, false},
		{"steer applied to queued", WorkflowInterventionModeSteer, WorkflowInterventionStateApplied, WorkflowInterventionStateQueued, false},
		{"steer rejected to applied", WorkflowInterventionModeSteer, WorkflowInterventionStateRejected, WorkflowInterventionStateApplied, false},
		{"stop requested to stop_requested", WorkflowInterventionModeStop, WorkflowInterventionStateRequested, WorkflowInterventionStateStopRequested, true},
		{"stop requested to confirmed", WorkflowInterventionModeStop, WorkflowInterventionStateStopRequested, WorkflowInterventionStateStopConfirmed, true},
		{"stop requested to unknown", WorkflowInterventionModeStop, WorkflowInterventionStateStopRequested, WorkflowInterventionStateStopUnknown, true},
		{"stop unknown to confirmed", WorkflowInterventionModeStop, WorkflowInterventionStateStopUnknown, WorkflowInterventionStateStopConfirmed, true},
		// A settled unknown stop must never re-request: that would be a second interrupt.
		{"stop unknown back to stop_requested", WorkflowInterventionModeStop, WorkflowInterventionStateStopUnknown, WorkflowInterventionStateStopRequested, false},
		{"stop confirmed to unknown", WorkflowInterventionModeStop, WorkflowInterventionStateStopConfirmed, WorkflowInterventionStateStopUnknown, false},
		// Mode/state mismatch beats graph legality.
		{"steer cannot reach stop_confirmed", WorkflowInterventionModeSteer, WorkflowInterventionStateRequested, WorkflowInterventionStateStopConfirmed, false},
		{"stop cannot reach applied", WorkflowInterventionModeStop, WorkflowInterventionStateRequested, WorkflowInterventionStateApplied, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.mode.ValidateTransition(tt.from, tt.to)
			if tt.ok && err != nil {
				t.Fatalf("%q → %q = %v, want legal", tt.from, tt.to, err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatalf("%q → %q was accepted, want a refusal", tt.from, tt.to)
				}
				if !errors.Is(err, ErrInvalidInterventionTransition) &&
					!errors.Is(err, ErrInterventionModeStateMismatch) {
					t.Fatalf("%q → %q err = %v, want a typed intervention error", tt.from, tt.to, err)
				}
			}
		})
	}
}

// TestInterventionChatFactLicenses is the core "no fabricated applied" unit proof. Each
// receipt state demands a different piece of evidence, and a zero fact licenses none of the
// evidence-bearing states.
func TestInterventionChatFactLicenses(t *testing.T) {
	t.Parallel()
	var zero InterventionChatFact
	for _, state := range []WorkflowInterventionState{
		WorkflowInterventionStateQueued,
		WorkflowInterventionStateDelivered,
		WorkflowInterventionStateApplied,
	} {
		if !state.RequiresChatFact() {
			t.Fatalf("%q must require a chat fact", state)
		}
		if zero.Licenses(state) {
			t.Fatalf("an empty chat fact licensed %q; applied/delivered/queued must never be fabricable", state)
		}
	}

	tests := []struct {
		name  string
		fact  InterventionChatFact
		state WorkflowInterventionState
		want  bool
	}{
		{
			"applied needs both turn ids",
			InterventionChatFact{ChatTurnID: "turn-1", ProviderTurnID: "prov-1"},
			WorkflowInterventionStateApplied, true,
		},
		{
			"applied without an AO turn id is refused",
			InterventionChatFact{ProviderTurnID: "prov-1"},
			WorkflowInterventionStateApplied, false,
		},
		{
			"applied without a provider turn id is refused",
			InterventionChatFact{ChatTurnID: "turn-1"},
			WorkflowInterventionStateApplied, false,
		},
		{
			"delivered from a running turn",
			InterventionChatFact{ChatTurnID: "turn-1", ChatTurnState: TurnStateRunning},
			WorkflowInterventionStateDelivered, true,
		},
		{
			"delivered from a provider turn id alone",
			InterventionChatFact{ProviderTurnID: "prov-1"},
			WorkflowInterventionStateDelivered, true,
		},
		{
			"delivered from a completed turn is refused",
			InterventionChatFact{ChatTurnID: "turn-1", ChatTurnState: TurnStateCompleted},
			WorkflowInterventionStateDelivered, false,
		},
		{
			"queued from the explicit queued answer",
			InterventionChatFact{Queued: true},
			WorkflowInterventionStateQueued, true,
		},
		{
			"queued from a queued turn row",
			InterventionChatFact{ChatTurnID: "turn-1", ChatTurnState: TurnStateQueued},
			WorkflowInterventionStateQueued, true,
		},
		{
			"queued is not licensed by a running turn",
			InterventionChatFact{ChatTurnID: "turn-1", ChatTurnState: TurnStateRunning},
			WorkflowInterventionStateQueued, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.fact.Licenses(tt.state); got != tt.want {
				t.Fatalf("Licenses(%q) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}

	// States that report an absence or a decision need no chat fact.
	for _, state := range []WorkflowInterventionState{
		WorkflowInterventionStateRejected, WorkflowInterventionStateCancelled,
		WorkflowInterventionStateUnknown, WorkflowInterventionStateStopRequested,
		WorkflowInterventionStateStopConfirmed, WorkflowInterventionStateStopUnknown,
		WorkflowInterventionStateRequested,
	} {
		if state.RequiresChatFact() {
			t.Fatalf("%q must not require a chat fact", state)
		}
		if !zero.Licenses(state) {
			t.Fatalf("%q must be reachable without chat evidence", state)
		}
	}
}

func TestWorkflowInterventionValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	attempt := WorkflowAttemptID("attempt-1")
	session := SessionID("p1-1")

	valid := WorkflowIntervention{
		ID: "intervention-1", WorkflowID: "workflow-1", TaskID: "task-1",
		AttemptID: &attempt, Mode: WorkflowInterventionModeSteer,
		State: WorkflowInterventionStateRequested, SessionID: &session,
		ControllerGeneration: "gen-1", ClientMessageID: "msg-1", OperationSHA: sha,
		TargetProviderTurnID: "prov-1", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}

	tests := []struct {
		name   string
		mutate func(*WorkflowIntervention)
		want   error
	}{
		{"valid steer", func(*WorkflowIntervention) {}, nil},
		{"valid stop", func(i *WorkflowIntervention) {
			i.Mode = WorkflowInterventionModeStop
			i.State = WorkflowInterventionStateStopRequested
			i.TargetProviderTurnID = ""
		}, nil},
		{"missing id", func(i *WorkflowIntervention) { i.ID = "" }, ErrInvalidIntervention},
		{"missing client message", func(i *WorkflowIntervention) { i.ClientMessageID = "" }, ErrInvalidIntervention},
		{"short sha", func(i *WorkflowIntervention) { i.OperationSHA = "abc" }, ErrInvalidIntervention},
		{"missing generation", func(i *WorkflowIntervention) { i.ControllerGeneration = "  " }, ErrInterventionGenerationRequired},
		{"steer without a target turn", func(i *WorkflowIntervention) { i.TargetProviderTurnID = "" }, ErrInterventionTurnRequired},
		{"bad mode", func(i *WorkflowIntervention) { i.Mode = "pause" }, ErrInvalidInterventionMode},
		{"bad state", func(i *WorkflowIntervention) { i.State = "sent" }, ErrInvalidInterventionState},
		{"steer claiming a stop state", func(i *WorkflowIntervention) {
			i.State = WorkflowInterventionStateStopConfirmed
		}, ErrInterventionModeStateMismatch},
		{"stop claiming applied", func(i *WorkflowIntervention) {
			i.Mode = WorkflowInterventionModeStop
			i.State = WorkflowInterventionStateApplied
		}, ErrInterventionModeStateMismatch},
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

// TestWorkflowInterventionAllowsNilSession proves the M2-03 lesson: no state may forbid a
// NULL session, because a session may be deleted while the intervention row survives as
// history.
func TestWorkflowInterventionAllowsNilSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	for _, state := range allInterventionStates {
		mode := WorkflowInterventionModeSteer
		if state.StopState() {
			mode = WorkflowInterventionModeStop
		}
		intervention := WorkflowIntervention{
			ID: "i-1", WorkflowID: "w-1", TaskID: "t-1",
			Mode: mode, State: state,
			// SessionID deliberately nil: this must be legal in every state.
			ControllerGeneration: "gen-1", ClientMessageID: "m-1",
			OperationSHA:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			TargetProviderTurnID: "prov-1",
			Revision:             1, CreatedAt: now, UpdatedAt: now,
		}
		if mode == WorkflowInterventionModeStop {
			intervention.TargetProviderTurnID = ""
		}
		if err := intervention.Validate(); err != nil {
			t.Fatalf("a nil session must be legal in state %q: %v", state, err)
		}
	}
}
