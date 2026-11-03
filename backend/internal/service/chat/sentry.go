package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// captureChatFault reports a Chat failure that never reaches an HTTP response
// (turn dispatch, event projection, provider errors, hibernation wake) to
// Sentry. A signed-out agent and canceled work are expected and stay out.
func captureChatFault(ctx context.Context, err error, harness domain.AgentHarness, operation, reason string) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ports.ErrChatAuthRequired) {
		return
	}
	extra := map[string]string{"agent": string(harness)}
	if reason != "" {
		extra["reason"] = reason
	}
	sentryobs.CaptureBackground(context.WithoutCancel(ctx), err, "chat", operation, extra)
}
