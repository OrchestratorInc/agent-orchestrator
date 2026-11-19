package hermesacp

import (
	"errors"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestPromptErrorFailureRestatesAdmittedTurnFailure(t *testing.T) {
	direct := acpsdk.NewInternalError(map[string]any{"details": "admitted_turn_failed"})
	// AO's persistent chat host wraps the agent's error data.
	relayed := acpsdk.NewInternalError(map[string]any{
		"ao.persistentEventId": "acp-host:1:3",
		"providerData":         map[string]any{"details": "admitted_turn_failed"},
	})
	tests := []struct {
		name     string
		err      error
		answered bool
		want     string
	}{
		{name: "a failure Hermes explained in its reply", err: relayed, answered: true, want: "Hermes could not finish this turn. Its reply above says why."},
		{name: "a failure without a reply", err: relayed, want: "Hermes could not finish this turn."},
		{name: "the unwrapped agent error", err: direct, answered: true, want: "Hermes could not finish this turn. Its reply above says why."},
		{name: "another internal error keeps the generic error", err: acpsdk.NewInternalError(map[string]any{"details": "session_replay_gap"})},
		{name: "an error without data keeps the generic error", err: acpsdk.NewInternalError(nil)},
		{name: "another error code keeps the generic error", err: acpsdk.NewInvalidParams(map[string]any{"details": "admitted_turn_failed"})},
		{name: "a non-ACP error keeps the generic error", err: errors.New("admitted_turn_failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promptErrorFailure(tt.err, tt.answered)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("promptErrorFailure = %q, want nil", got)
				}
				return
			}
			if got == nil || got.Error() != tt.want {
				t.Fatalf("promptErrorFailure = %v, want %q", got, tt.want)
			}
			var requestErr *acpsdk.RequestError
			if !errors.As(got, &requestErr) {
				t.Fatalf("restated failure dropped the agent error: %#v", got)
			}
		})
	}
}
