package sessionmanager

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func captureBackgroundFault(ctx context.Context, err error, operation, reason string) {
	if isExpectedSessionFailure(err) {
		return
	}
	extra := map[string]string{}
	if reason != "" {
		extra["reason"] = reason
	}
	sentryobs.CaptureBackground(ctx, err, "session_manager", operation, extra)
}

func isExpectedSessionFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, ports.ErrAgentAuthRequired) || errors.Is(err, ports.ErrChatAuthRequired) ||
		errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired) {
		return true
	}
	var setupErr *SetupStepError
	return errors.As(err, &setupErr)
}
