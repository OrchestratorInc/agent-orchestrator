package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"

	turnfailuresvc "github.com/aoagents/agent-orchestrator/backend/internal/service/turnfailure"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Only errors SendSemantic returns before writing anything count as refusals.
// Errors that can follow a typed prompt keep the failure notice's recipient.
func TestTurnFailureDeliveryMarksOnlyRefusalsBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		refused bool
	}{
		{"accepted", nil, false},
		{"missing session", sessionmanager.ErrNotFound, true},
		{"draft in composer", fmt.Errorf("send ao-1: %w", sessionmanager.ErrComposerBusy), true},
		{"acceptance unsupported", sessionmanager.ErrSemanticAcceptanceUnsupported, true},
		{"acceptance timed out after the write",
			fmt.Errorf("%w: %w", sessionmanager.ErrSemanticAcceptanceUnsupported, sessionmanager.ErrPromptSubmissionUnobserved), false},
		{"wait cancelled after the write", context.Canceled, false},
		{"session ended while waiting", sessionmanager.ErrTerminated, false},
		{"store read failed while waiting", errors.New("database is locked"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivery := turnFailureDelivery{sessions: &reportDeliverySession{sendErr: tc.err}}
			err := delivery.SendSemantic(context.Background(), "ao-1", "notice", "worker-turn-failed:t")
			if got := errors.Is(err, turnfailuresvc.ErrRefusedDelivery); got != tc.refused {
				t.Fatalf("refused = %v, want %v (err %v)", got, tc.refused, err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("error %v lost its cause %v", err, tc.err)
			}
		})
	}
}
