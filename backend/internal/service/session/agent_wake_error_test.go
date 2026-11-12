package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func TestAgentWakeFailureMapsToTypedConflict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		queued   bool
		contains []string
	}{
		{
			name:     "chat keeps the message queued",
			err:      fmt.Errorf("send s1: %w", &ports.AgentWakeError{Reason: errors.New("codex is not installed"), Queued: true}),
			queued:   true,
			contains: []string{"could not restart it: codex is not installed", "queued"},
		},
		{
			name:     "terminal did not deliver",
			err:      fmt.Errorf("send s1: %w", &ports.AgentWakeError{Reason: errors.New("workspace is gone")}),
			contains: []string{"workspace is gone", "not delivered"},
		},
		{
			// A known resume failure reuses the message resume-agent gives it,
			// and the wake error wins over the reason's own code.
			name:     "known reason keeps its user-facing text",
			err:      &ports.AgentWakeError{Reason: fmt.Errorf("resume agent s1: %w", sessionmanager.ErrResumeInProgress), Queued: true},
			queued:   true,
			contains: []string{"The agent is already being resumed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var apiErr *apierr.Error
			if !errors.As(mapSessionError(tc.err), &apiErr) {
				t.Fatalf("mapSessionError(%v) is not an API error", tc.err)
			}
			if apiErr.Code != "AGENT_WAKE_FAILED" || apiErr.Kind != apierr.KindConflict {
				t.Fatalf("code/kind = %s/%v, want AGENT_WAKE_FAILED conflict", apiErr.Code, apiErr.Kind)
			}
			if apiErr.Details["queued"] != tc.queued {
				t.Fatalf("details = %v, want queued=%v", apiErr.Details, tc.queued)
			}
			for _, want := range tc.contains {
				if !strings.Contains(apiErr.Message, want) {
					t.Fatalf("message = %q, want it to mention %q", apiErr.Message, want)
				}
			}
		})
	}
}

// Plain refusals are unchanged: an exited agent without a trusted sender is
// still AGENT_EXITED, a terminated session still SESSION_TERMINATED.
func TestPlainExitedAndTerminatedRefusalsUnchanged(t *testing.T) {
	for err, code := range map[error]string{
		fmt.Errorf("send s1: %w", sessionmanager.ErrAgentExited): "AGENT_EXITED",
		fmt.Errorf("send s1: %w", sessionmanager.ErrTerminated):  "SESSION_TERMINATED",
	} {
		var apiErr *apierr.Error
		if !errors.As(mapSessionError(err), &apiErr) || apiErr.Code != code {
			t.Fatalf("mapSessionError(%v) = %v, want %s", err, apiErr, code)
		}
	}
}
