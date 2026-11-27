package acp

import (
	"errors"
	"fmt"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Spawn telemetry tells an agent that refused the start apart from one that
// went away: only an error the agent sent back is marked agent_error.
func TestMarkAgentAnsweredOnlyMarksErrorsTheAgentSent(t *testing.T) {
	answered := &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": "model not found"}}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"agent error", fmt.Errorf("ACP session/new: %w", answered), true},
		{"agent method not found", fmt.Errorf("%w: ACP initialize: %w", ports.ErrChatDriverIncompatible, acpsdk.NewMethodNotFound("initialize")), true},
		{"peer disconnected", fmt.Errorf("ACP session/new: %w", acpsdk.NewInternalError(map[string]any{"error": "peer disconnected before response"})), false},
		{"request cancelled locally", acpsdk.NewRequestCancelled(map[string]any{"error": "context canceled"}), false},
		{"not a JSON-RPC error", errors.New("configure ACP session: boom"), false},
	} {
		marked := markAgentAnswered(tc.err)
		var cause interface{ FailureCause() string }
		got := errors.As(marked, &cause) && cause.FailureCause() == "agent_error"
		if got != tc.want {
			t.Errorf("%s: marked = %v, want %v", tc.name, got, tc.want)
		}
		if marked.Error() != tc.err.Error() || !errors.Is(marked, tc.err) {
			t.Errorf("%s: marking changed the error: %v", tc.name, marked)
		}
	}
	if !errors.Is(markAgentAnswered(fmt.Errorf("%w: x: %w", ports.ErrChatDriverIncompatible, answered)), ports.ErrChatDriverIncompatible) {
		t.Fatal("marked error lost its sentinel")
	}
}
