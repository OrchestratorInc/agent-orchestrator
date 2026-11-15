package domain

import "testing"

func TestActivityState_StickyAndNeedsInput(t *testing.T) {
	tests := []struct {
		state      ActivityState
		sticky     bool
		needsInput bool
	}{
		{ActivityActive, false, false},
		{ActivityIdle, false, false},
		{ActivityWaitingInput, true, true},
		{ActivityBlocked, true, true},
		{ActivityExited, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := tt.state.IsSticky(); got != tt.sticky {
				t.Errorf("IsSticky() = %v, want %v", got, tt.sticky)
			}
			if got := tt.state.NeedsInput(); got != tt.needsInput {
				t.Errorf("NeedsInput() = %v, want %v", got, tt.needsInput)
			}
		})
	}
}
