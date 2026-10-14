package session

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// SwitchAgentInput is the controller-facing command for replacing the active
// provider while retaining the logical AO session.
type SwitchAgentInput struct {
	TargetHarness  domain.AgentHarness
	Model          string
	IdempotencyKey string
}

// SwitchAgent starts or resumes a durable agent-switch saga for a session.
func (s *Service) SwitchAgent(ctx context.Context, id domain.SessionID, in SwitchAgentInput) (domain.AgentSwitch, error) {
	switchRecord, err := s.manager.SwitchAgent(ctx, id, sessionmanager.SwitchAgentConfig{
		TargetHarness:  in.TargetHarness,
		Model:          in.Model,
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		apiErr := toAPIError(err)
		s.recordAgentSwitched(ctx, id, domain.AgentSwitch{IdempotencyKey: in.IdempotencyKey, TargetHarness: in.TargetHarness}, actionStatusForError(err), apiErr)
		return domain.AgentSwitch{}, apiErr
	}
	status := domain.ActivityStatusRunning
	switch switchRecord.State {
	case domain.AgentSwitchCompleted:
		status = domain.ActivityStatusCompleted
	case domain.AgentSwitchFailed:
		status = domain.ActivityStatusFailed
	}
	s.recordAgentSwitched(ctx, id, switchRecord, status, nil)
	return switchRecord, nil
}

// observeSettledAgentSwitch updates the activity admitted with the switch once
// the daemon-owned worker reaches a durable terminal state. The admitting POST
// only sees preparing_handoff.
func (s *Service) observeSettledAgentSwitch(ctx context.Context, sw domain.AgentSwitch) {
	if s == nil || !sw.State.Terminal() || sw.ID == "" {
		return
	}
	status := domain.ActivityStatusCompleted
	var err error
	if sw.State == domain.AgentSwitchFailed {
		status = domain.ActivityStatusFailed
		if sw.ErrorCode != "" {
			err = errors.New(string(sw.ErrorCode))
		}
	}
	s.recordAgentSwitched(context.WithoutCancel(ctx), sw.SessionID, sw, status, err)
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
