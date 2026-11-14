package shellterm

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const MaxSessionTerminalOutputLines = 2000

func (s *Service) findSessionShell(ctx context.Context, sessionID domain.SessionID, handleID string) (ports.RuntimeHandle, error) {
	handleID = strings.TrimSpace(handleID)
	if sessionID == "" || handleID == "" {
		return ports.RuntimeHandle{}, apierr.Invalid("PANE_TERMINAL_REQUIRED", "A session and a terminal tab key are required", nil)
	}
	records, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("find session terminal: %w", err)
	}
	for _, rec := range records {
		if rec.HandleID == handleID && rec.SessionID == sessionID {
			return ports.RuntimeHandle{ID: rec.HandleID}, nil
		}
	}
	return ports.RuntimeHandle{}, apierr.NotFound("PANE_TERMINAL_NOT_FOUND",
		fmt.Sprintf("This session has no terminal %q. Run `ao pane` to list its terminal tab keys.", handleID))
}

func (s *Service) sessionShell(ctx context.Context, sessionID domain.SessionID, handleID string) (ports.RuntimeHandle, error) {
	handle, err := s.findSessionShell(ctx, sessionID, handleID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if alive, aliveErr := s.runtime.IsChildAlive(ctx, handle); aliveErr == nil && !alive {
		return ports.RuntimeHandle{}, apierr.Conflict("PANE_TERMINAL_EXITED", "That terminal's shell has exited; close it with `ao pane close` or open a new one with `ao pane split terminal`", nil)
	}
	return handle, nil
}

func (s *Service) CloseSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID string) error {
	handle, err := s.findSessionShell(ctx, sessionID, handleID)
	if err != nil {
		return err
	}
	return s.CloseShellTerminal(ctx, handle.ID)
}

func (s *Service) TypeIntoSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID, text string, enter bool) error {
	handle, err := s.sessionShell(ctx, sessionID, handleID)
	if err != nil {
		return err
	}
	if enter {
		return s.runtime.SendMessage(ctx, handle, text)
	}
	if text == "" {
		return apierr.Invalid("PANE_TERMINAL_TEXT_REQUIRED", "Nothing to type", nil)
	}
	return s.runtime.SendInput(ctx, handle, text)
}

func (s *Service) InterruptSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID string) error {
	handle, err := s.sessionShell(ctx, sessionID, handleID)
	if err != nil {
		return err
	}
	return s.runtime.SendInput(ctx, handle, "\x03")
}

func (s *Service) SessionTerminalOutput(ctx context.Context, sessionID domain.SessionID, handleID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	lines = min(lines, MaxSessionTerminalOutputLines)
	handle, err := s.sessionShell(ctx, sessionID, handleID)
	if err != nil {
		return "", err
	}
	var output string
	renderedRead := false
	if styled, ok := s.runtime.(ports.StyledTerminalOutputReader); ok {
		if rendered, styledErr := styled.GetStyledOutput(ctx, handle, lines); styledErr == nil {
			output = terminalui.PlainTerminalText(rendered)
			renderedRead = true
		}
	}
	if !renderedRead {
		raw, rawErr := s.runtime.GetOutput(ctx, handle, lines)
		if rawErr != nil {
			return "", apierr.Unavailable("PANE_TERMINAL_OUTPUT_UNAVAILABLE", "Could not read that terminal's screen")
		}
		output = terminalui.PlainTerminalText(raw)
	}
	trimmed := strings.Split(output, "\n")
	for i, line := range trimmed {
		trimmed[i] = strings.TrimRight(line, " \t\r")
	}
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == "" {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) > lines {
		trimmed = trimmed[len(trimmed)-lines:]
	}
	return strings.TrimRight(strings.Join(trimmed, "\n"), "\n"), nil
}
