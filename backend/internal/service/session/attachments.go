package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (s *Service) StageAttachments(
	ctx context.Context,
	id domain.SessionID,
	attachments []ports.SpawnAttachment,
) ([]string, error) {
	return s.manager.StageAttachments(ctx, id, attachments)
}
