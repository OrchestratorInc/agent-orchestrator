package shellterm

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type AuthTerminalObservation struct {
	Output string
	Alive  bool
}

func (s *Service) ObserveAuthTerminal(ctx context.Context, handleID string) (AuthTerminalObservation, error) {
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return AuthTerminalObservation{}, err
	}
	if !found || !rec.Transient || rec.AppRunID != s.appRunID {
		return AuthTerminalObservation{}, apierr.NotFound("AGENT_AUTH_TERMINAL_NOT_FOUND", "Authentication terminal not found.")
	}
	handle := ports.RuntimeHandle{ID: handleID}
	output, err := s.runtime.GetOutput(ctx, handle, 100)
	if err != nil {
		return AuthTerminalObservation{}, err
	}
	if styled, ok := s.runtime.(interface {
		GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error)
	}); ok {
		if rendered, styledErr := styled.GetStyledOutput(ctx, handle, 100); styledErr == nil && rendered != "" {
			output = rendered
		}
	}
	if len(output) > 16*1024 {
		output = output[len(output)-16*1024:]
	}
	alive, err := s.runtime.IsChildAlive(ctx, handle)
	if err != nil {
		return AuthTerminalObservation{}, err
	}
	return AuthTerminalObservation{Output: output, Alive: alive}, nil
}

func (s *Service) SendAuthTerminalKey(ctx context.Context, handleID, action string) error {
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return err
	}
	if !found || !rec.Transient || rec.AppRunID != s.appRunID {
		return apierr.NotFound("AGENT_AUTH_TERMINAL_NOT_FOUND", "Authentication terminal not found.")
	}
	var input string
	switch action {
	case "previous":
		input = "\x1b[A"
	case "next":
		input = "\x1b[B"
	case "select":
		input = "\r"
	default:
		if len(action) != 1 || action < "1" || action > "9" {
			return apierr.Invalid("AGENT_AUTH_ACTION_INVALID", "Choose a supported login menu action.", nil)
		}
		input = action
	}
	return s.runtime.SendInput(ctx, ports.RuntimeHandle{ID: handleID}, input)
}
