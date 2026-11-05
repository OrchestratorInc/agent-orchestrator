package ports_test

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Fencing contracts on the gateway port.
//
// These are the rules the execution and intervention coordinators branch on, so they are
// tested here rather than only through a gateway implementation: a fence that passes when
// it should refuse is exactly the bug that lets work land on a replaced controller.

// TestValidateFenceRefusesAnUnobservedGeneration is the R-01 case: the caller named a
// controller, and the observation carries none.
//
// The earlier rule compared generations only when BOTH sides were non-empty, so this exact
// shape passed. It is the shape a rotated or partially written session row produces, which
// made "no evidence" authorize work the caller had no evidence for.
func TestValidateFenceRefusesAnUnobservedGeneration(t *testing.T) {
	observation := ports.SessionObservation{
		SessionID:            "s-1",
		ControllerGeneration: "",
	}
	err := observation.ValidateFence("s-1", "gen-1")
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want ErrExecutionStaleGeneration", err)
	}
}

// TestValidateFenceTable pins every combination, including the one that stays permissive:
// a caller with no expectation is asking for no fence, so both-empty must remain a pass.
func TestValidateFenceTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		observedID domain.SessionID
		observed   string
		expectedID domain.SessionID
		expected   string
		want       error
	}{
		{
			name:       "matching generation passes",
			observedID: "s-1", observed: "gen-1", expectedID: "s-1", expected: "gen-1",
		},
		{
			name:       "different generation refuses",
			observedID: "s-1", observed: "gen-2", expectedID: "s-1", expected: "gen-1",
			want: ports.ErrExecutionStaleGeneration,
		},
		{
			name:       "expected generation with none observed refuses",
			observedID: "s-1", observed: "", expectedID: "s-1", expected: "gen-1",
			want: ports.ErrExecutionStaleGeneration,
		},
		{
			name:       "both empty stays permissive",
			observedID: "s-1", observed: "", expectedID: "s-1", expected: "",
		},
		{
			name:       "no expectation with an observed generation passes",
			observedID: "s-1", observed: "gen-1", expectedID: "s-1", expected: "",
		},
		{
			name:       "different session refuses before the generation",
			observedID: "s-2", observed: "gen-1", expectedID: "s-1", expected: "gen-1",
			want: ports.ErrExecutionSessionMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := ports.SessionObservation{SessionID: tc.observedID, ControllerGeneration: tc.observed}
			err := observation.ValidateFence(tc.expectedID, tc.expected)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestValidateFenceStillRefusesATerminatedSession proves the termination check survives the
// generation change: a terminated session refuses even when the generation matches.
func TestValidateFenceStillRefusesATerminatedSession(t *testing.T) {
	observation := ports.SessionObservation{
		SessionID: "s-1", ControllerGeneration: "gen-1", Terminated: true,
	}
	if err := observation.ValidateFence("s-1", "gen-1"); !errors.Is(err, ports.ErrExecutionTerminated) {
		t.Fatalf("err = %v, want ErrExecutionTerminated", err)
	}
}

// TestMessageRequestRequiresAGenerationForBothDeliveries proves a send cannot be unfenced.
func TestMessageRequestRequiresAGenerationForBothDeliveries(t *testing.T) {
	base := ports.MessageRequest{
		SessionID:       "s-1",
		ClientMessageID: "m-1",
		Text:            "hello",
	}
	for _, tc := range []struct {
		name       string
		delivery   ports.MessageDelivery
		targetTurn string
		generation string
		want       error
	}{
		{
			name:     "send with a generation passes",
			delivery: ports.MessageDeliverySend, generation: "gen-1",
		},
		{
			name:     "send without a generation is refused",
			delivery: ports.MessageDeliverySend, want: ports.ErrExecutionRefused,
		},
		{
			name:     "steer with a generation passes",
			delivery: ports.MessageDeliverySteer, targetTurn: "turn-1", generation: "gen-1",
		},
		{
			name:     "steer without a generation is refused",
			delivery: ports.MessageDeliverySteer, targetTurn: "turn-1", want: ports.ErrExecutionRefused,
		},
		{
			name:     "steer without a turn is refused",
			delivery: ports.MessageDeliverySteer, generation: "gen-1", want: ports.ErrExecutionRefused,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := base
			request.Delivery = tc.delivery
			request.TargetProviderTurnID = tc.targetTurn
			request.ControllerGeneration = tc.generation
			err := request.Validate()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestInterruptRequestRequiresAGeneration proves a stop cannot be unfenced.
func TestInterruptRequestRequiresAGeneration(t *testing.T) {
	if err := (ports.InterruptRequest{SessionID: "s-1"}).Validate(); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if err := (ports.InterruptRequest{SessionID: "s-1", ControllerGeneration: "gen-1"}).Validate(); err != nil {
		t.Fatalf("err = %v, want nil for a fenced request", err)
	}
}

// TestMessageReceiptMatchesSend is the send-side receipt contract.
func TestMessageReceiptMatchesSend(t *testing.T) {
	request := ports.MessageRequest{
		SessionID: "s-1", ClientMessageID: "m-1",
		Delivery: ports.MessageDeliverySend, ControllerGeneration: "gen-1",
	}
	for _, tc := range []struct {
		name    string
		receipt ports.MessageReceipt
		want    bool
	}{
		{
			name: "dispatched turn matches",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1", TurnID: "turn-1",
				ProviderTurnID: "prov-1", ControllerGeneration: "gen-1",
			},
			want: true,
		},
		{
			name: "explicitly queued matches",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1", TurnID: "turn-1",
				Queued: true, ControllerGeneration: "gen-1",
			},
			want: true,
		},
		{
			name: "duplicate matches",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1",
				Duplicate: true, ControllerGeneration: "gen-1",
			},
			want: true,
		},
		{
			name: "different generation does not match",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1", TurnID: "turn-1",
				ControllerGeneration: "gen-2",
			},
		},
		{
			name: "missing generation does not match",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1", TurnID: "turn-1",
			},
		},
		{
			name: "different session does not match",
			receipt: ports.MessageReceipt{
				SessionID: "s-2", ClientMessageID: "m-1", TurnID: "turn-1",
				ControllerGeneration: "gen-1",
			},
		},
		{
			name: "different client message id does not match",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-2", TurnID: "turn-1",
				ControllerGeneration: "gen-1",
			},
		},
		{
			name: "a bare acknowledgement does not match",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1", ControllerGeneration: "gen-1",
			},
		},
		{
			name: "a steer receipt does not match a send",
			receipt: ports.MessageReceipt{
				SessionID: "s-1", ClientMessageID: "m-1", TurnID: "turn-1",
				Steered: true, TargetProviderTurnID: "prov-1", ControllerGeneration: "gen-1",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.receipt.MatchesSend(request); got != tc.want {
				t.Fatalf("MatchesSend = %v, want %v for %+v", got, tc.want, tc.receipt)
			}
		})
	}
}

// TestInterruptReceiptMatchesInterrupt proves a stop receipt must name the generation it is
// about before it can confirm anything.
func TestInterruptReceiptMatchesInterrupt(t *testing.T) {
	request := ports.InterruptRequest{SessionID: "s-1", ControllerGeneration: "gen-1"}
	for _, tc := range []struct {
		name    string
		receipt ports.InterruptReceipt
		want    bool
	}{
		{
			name:    "confirmed on the named generation matches",
			receipt: ports.InterruptReceipt{SessionID: "s-1", ControllerGeneration: "gen-1", Confirmed: true},
			want:    true,
		},
		{
			name:    "nothing-to-stop on the named generation matches",
			receipt: ports.InterruptReceipt{SessionID: "s-1", ControllerGeneration: "gen-1", NothingToStop: true},
			want:    true,
		},
		{
			name:    "another generation does not match",
			receipt: ports.InterruptReceipt{SessionID: "s-1", ControllerGeneration: "gen-2", Confirmed: true},
		},
		{
			name:    "no generation does not match",
			receipt: ports.InterruptReceipt{SessionID: "s-1", Confirmed: true},
		},
		{
			name:    "another session does not match",
			receipt: ports.InterruptReceipt{SessionID: "s-2", ControllerGeneration: "gen-1", Confirmed: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.receipt.MatchesInterrupt(request); got != tc.want {
				t.Fatalf("MatchesInterrupt = %v, want %v for %+v", got, tc.want, tc.receipt)
			}
		})
	}
}
