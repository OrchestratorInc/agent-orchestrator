package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// StageAttachments writes files into a session's worktree and returns their
// worktree-relative paths, for a caller that will name those paths in the message
// it is about to send.
//
// It is a separate step from sending rather than a field on the message because
// the transport is a path reference, not the bytes: the conversation port carries
// text, and a file reaches the agent by existing in the worktree the agent can
// read. Staging first also means a failed write is reported before a message
// claiming to have attachments is recorded in the timeline.
func (s *Service) StageAttachments(
	ctx context.Context,
	id domain.SessionID,
	attachments []ports.SpawnAttachment,
) ([]string, error) {
	// Files staged into a held preparation live only in its worktree; staging
	// counts as use so the host does not expire it under the page. Ids that
	// are not preparations are ignored.
	if renewer, ok := s.manager.(taskPreparationRenewer); ok {
		renewer.RenewTaskPreparation(domain.TaskPreparationToken(id))
	}
	return s.manager.StageAttachments(ctx, id, attachments)
}
