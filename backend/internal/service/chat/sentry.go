package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func captureChatFault(ctx context.Context, err error, harness domain.AgentHarness, operation, reason string) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ports.ErrAgentAuthRequired) || errors.Is(err, ports.ErrChatAuthRequired) ||
		domain.ParseConversationUsageLimit(err.Error(), nil) != nil {
		return
	}
	extra := map[string]string{"agent": string(harness)}
	if reason != "" {
		extra["reason"] = reason
	}
	sentryobs.CaptureBackground(context.WithoutCancel(ctx), err, "chat", operation, extra)
}
