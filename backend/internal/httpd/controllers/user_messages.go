package controllers

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type UserMessageRecorder interface {
	RecordUserMessage(ctx context.Context, id domain.SessionID, isSteer bool)
}

func recordUserMessage(ctx context.Context, recorder UserMessageRecorder, id domain.SessionID, senderSessionID string, isSteer bool) {
	if recorder == nil || senderSessionID != "" {
		return
	}
	recorder.RecordUserMessage(ctx, id, isSteer)
}
