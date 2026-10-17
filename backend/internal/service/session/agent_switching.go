package session

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// SwitchAgentInput is the controller-facing command for replacing the active
// provider while retaining the logical AO session.
type SwitchAgentInput struct {
	TargetHarness  domain.AgentHarness
	Model          string
	Effort         string
	IdempotencyKey string
	// Message, when set, opens the target agent's first turn (Chat only).
	Message *ports.ChatUserMessage
}

// SwitchAgent starts or resumes a durable agent-switch saga for a session.
func (s *Service) SwitchAgent(ctx context.Context, id domain.SessionID, in SwitchAgentInput) (domain.AgentSwitch, error) {
	switchRecord, err := s.manager.SwitchAgent(ctx, id, sessionmanager.SwitchAgentConfig{
		TargetHarness:  in.TargetHarness,
		Model:          in.Model,
		Effort:         in.Effort,
		IdempotencyKey: in.IdempotencyKey,
		Message:        in.Message,
	})
	return switchRecord, toAPIError(err)
}

// switchPrewarmer starts a chat's picked-but-unsent switch target early.
type switchPrewarmer interface {
	PrewarmChatSwitch(ctx context.Context, id domain.SessionID, cfg sessionmanager.SwitchAgentConfig) error
}

// agentSwitchPrewarmTimeout bounds one background prewarm, Cursor's warm-up
// included.
const agentSwitchPrewarmTimeout = time.Minute

// PrewarmAgentSwitch starts the agent picked for a chat's next switch in the
// background and returns at once. Failures only cost the head start: the
// switch then starts its agent as usual.
func (s *Service) PrewarmAgentSwitch(id domain.SessionID, in SwitchAgentInput) {
	prewarmer, ok := s.manager.(switchPrewarmer)
	if !ok {
		return
	}
	base := s.backgroundContext
	if base == nil {
		base = context.Background()
	}
	work := func() {
		ctx, cancel := context.WithTimeout(base, agentSwitchPrewarmTimeout)
		defer cancel()
		err := prewarmer.PrewarmChatSwitch(ctx, id, sessionmanager.SwitchAgentConfig{
			TargetHarness: in.TargetHarness,
			Model:         in.Model,
			Effort:        in.Effort,
		})
		if err != nil && s.logger != nil {
			s.logger.Info("agent switch prewarm failed", "sessionID", id, "target", in.TargetHarness, "error", err)
		}
	}
	if s.runBackground != nil {
		s.runBackground(work)
		return
	}
	go work()
}

// RecoverAgentSwitch retries a durable source restoration without restarting
// the target side of the failed switch.
func (s *Service) RecoverAgentSwitch(ctx context.Context, id domain.SessionID, switchID domain.AgentSwitchID) (domain.AgentSwitch, error) {
	switchRecord, err := s.manager.RecoverAgentSwitch(ctx, id, switchID)
	return switchRecord, toAPIError(err)
}

// ListAgentSwitches returns the session's durable switch history, newest first.
func (s *Service) ListAgentSwitches(ctx context.Context, id domain.SessionID) ([]domain.AgentSwitch, error) {
	switches, err := s.manager.ListAgentSwitches(ctx, id)
	return switches, toAPIError(err)
}

// SubmitAgentHandoff records a generation-fenced, source-authored JSON handoff.
func (s *Service) SubmitAgentHandoff(
	ctx context.Context,
	id domain.SessionID,
	switchID domain.AgentSwitchID,
	sourceGenerationID domain.AgentGenerationID,
	handoff json.RawMessage,
) (domain.AgentSwitch, error) {
	switchRecord, err := s.manager.SubmitAgentHandoff(ctx, id, switchID, sourceGenerationID, handoff)
	return switchRecord, toAPIError(err)
}
