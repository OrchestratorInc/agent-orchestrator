package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrSkillsUnsupported reports a driver whose provider cannot enumerate skills.
// Distinct from an empty list: "this agent has no concept of skills" and "this
// agent has none installed" are the same thing to render but not the same thing
// to be wrong about, and only the first is permanent.
var ErrSkillsUnsupported = errors.New("chat driver cannot list skills")

// Skills reports the named skills the provider will let this session invoke.
//
// Read from the live conversation for the same reason models are: skills come from
// the user's own Codex config and the repo's own files, both of which change
// without AO being told. A list AO cached at build time would offer commands that
// no longer exist and hide ones the user just wrote.
func (s *Service) Skills(ctx context.Context, id domain.SessionID) ([]ports.ChatSkill, error) {
	return s.SkillsForOwner(ctx, domain.SessionConversationOwner(id))
}

// SkillsForOwner reports the skills the provider behind an owner's conversation
// will let it invoke.
func (s *Service) SkillsForOwner(ctx context.Context, owner domain.ConversationOwner) ([]ports.ChatSkill, error) {
	controller, _, err := s.controllerForOwner(ctx, owner)
	if err != nil {
		return nil, err
	}
	lister, ok := controller.conv.(ports.ChatSkillLister)
	if !ok {
		return nil, ErrSkillsUnsupported
	}
	return lister.ListSkills(ctx)
}
