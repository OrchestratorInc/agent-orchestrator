package shellterm

import (
	"context"
	"regexp"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// AuthTerminalObservation stays inside the daemon. In particular, Output must
// never be serialized by a controller: a native CLI can echo credentials.
type AuthTerminalObservation struct {
	Output string
	Alive  bool
}

// ObserveAuthTerminal reads only a trusted terminal from this app launch. The
// authentication service reduces its output to reviewed UI fields before any
// response crosses the HTTP boundary.
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

// authTerminalCommand is the only text a guided login may type: a plain
// slash command from a reviewed login plan, such as /logout.
var authTerminalCommand = regexp.MustCompile(`^/[a-z][a-z-]{0,31}$`)

// SendAuthTerminalCommand types a reviewed slash command into a trusted
// authentication terminal, without pressing Enter. It is daemon-internal:
// only login plans call it, never the HTTP API.
func (s *Service) SendAuthTerminalCommand(ctx context.Context, handleID, command string) error {
	if !authTerminalCommand.MatchString(command) {
		return apierr.Invalid("AGENT_AUTH_ACTION_INVALID", "Choose a supported login command.", nil)
	}
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return err
	}
	if !found || !rec.Transient || rec.AppRunID != s.appRunID {
		return apierr.NotFound("AGENT_AUTH_TERMINAL_NOT_FOUND", "Authentication terminal not found.")
	}
	return s.runtime.SendInput(ctx, ports.RuntimeHandle{ID: handleID}, command)
}

// authTerminalCode is an OAuth authorization code a user pastes back into a
// guided login: URL-safe characters on one line, nothing a terminal treats as
// a control sequence.
var authTerminalCode = regexp.MustCompile(`^[A-Za-z0-9/_.~+=%-]+$`)

// SendAuthTerminalCode types a user's authorization code into a trusted
// authentication terminal, without pressing Enter.
func (s *Service) SendAuthTerminalCode(ctx context.Context, handleID, code string) error {
	if len(code) < 8 || len(code) > 2048 || !authTerminalCode.MatchString(code) {
		return apierr.Invalid("AGENT_AUTH_CODE_INVALID", "Paste the authorization code exactly as shown.", nil)
	}
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return err
	}
	if !found || !rec.Transient || rec.AppRunID != s.appRunID {
		return apierr.NotFound("AGENT_AUTH_TERMINAL_NOT_FOUND", "Authentication terminal not found.")
	}
	return s.runtime.SendInput(ctx, ports.RuntimeHandle{ID: handleID}, code)
}

// SendAuthTerminalKey accepts only the fixed menu actions exposed by guided
// login: arrow navigation, Enter, and the digits 1-9 for numbered pickers. No
// renderer-provided bytes or credential text reach the PTY.
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
