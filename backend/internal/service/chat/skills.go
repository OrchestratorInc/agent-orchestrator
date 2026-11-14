package chat

import (
	"context"
	"errors"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ErrSkillsUnsupported = errors.New("chat driver cannot list skills")

func (s *Service) Skills(ctx context.Context, id domain.SessionID) ([]ports.ChatSkill, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return nil, err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return nil, err
	}
	lister, ok := controller.conv.(ports.ChatSkillLister)
	if !ok {
		return nil, ErrSkillsUnsupported
	}
	skills, err := lister.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return withSkillMetadata(skills, skillRoots(record.Metadata.WorkspacePath, home)), nil
}
