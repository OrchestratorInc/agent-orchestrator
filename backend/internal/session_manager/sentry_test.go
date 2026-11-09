package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestIsExpectedSessionFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, true},
		{"cancelled", context.Canceled, true},
		{"agent sign in", fmt.Errorf("spawn: %w", ports.ErrAgentAuthRequired), true},
		{"chat sign in", ports.ErrChatAuthRequired, true},
		{"discovery sign in", ports.ErrAgentModelDiscoverySignInRequired, true},
		{"user setup", fmt.Errorf("restore: %w", &SetupStepError{Err: errors.New("script failed")}), true},
		{"deadline", context.DeadlineExceeded, false},
		{"unexpected", errors.New("storage failed"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isExpectedSessionFailure(test.err); got != test.expected {
				t.Fatalf("expected state = %v, want %v", got, test.expected)
			}
		})
	}
}
