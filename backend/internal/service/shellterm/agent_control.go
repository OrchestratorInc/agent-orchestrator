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

// Agents drive their session's shell terminals with `ao pane run|send|read|
// interrupt`. Each call is scoped to one session: a session can only reach
// the shells opened in its own tab strip, never another session's shells or
// AO's command terminals (logins, setup).

// MaxSessionTerminalOutputLines bounds one read of a terminal's screen.
const MaxSessionTerminalOutputLines = 2000

// sessionShell returns sessionID's live shell terminal with handleID.
func (s *Service) sessionShell(ctx context.Context, sessionID domain.SessionID, handleID string) (ports.RuntimeHandle, error) {
	handleID = strings.TrimSpace(handleID)
	if sessionID == "" || handleID == "" {
		return ports.RuntimeHandle{}, apierr.Invalid("PANE_TERMINAL_REQUIRED", "A session and a terminal tab key are required", nil)
	}
	records, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("find session terminal: %w", err)
	}
	for _, rec := range records {
		if rec.HandleID != handleID || rec.SessionID != sessionID {
			continue
		}
		handle := ports.RuntimeHandle{ID: rec.HandleID}
		if alive, aliveErr := s.runtime.IsChildAlive(ctx, handle); aliveErr == nil && !alive {
			return ports.RuntimeHandle{}, apierr.Conflict("PANE_TERMINAL_EXITED", "That terminal's shell has exited; open a new one with `ao pane split terminal`", nil)
		}
		return handle, nil
	}
	return ports.RuntimeHandle{}, apierr.NotFound("PANE_TERMINAL_NOT_FOUND",
		fmt.Sprintf("This session has no terminal %q. Run `ao pane` to list its terminal tab keys.", handleID))
}

// TypeIntoSessionTerminal types text into one of the session's shell
// terminals, followed by Enter when enter is true.
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

// InterruptSessionTerminal sends Ctrl-C to one of the session's shell terminals.
func (s *Service) InterruptSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID string) error {
	handle, err := s.sessionShell(ctx, sessionID, handleID)
	if err != nil {
		return err
	}
	return s.runtime.SendInput(ctx, handle, "\x03")
}

// SessionTerminalOutput returns the last lines one of the session's shell
// terminals shows, as plain text: the rendered screen when the runtime keeps
// one, else its raw output with control sequences removed.
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
	if styled, ok := s.runtime.(ports.StyledTerminalOutputReader); ok {
		if rendered, styledErr := styled.GetStyledOutput(ctx, handle, lines); styledErr == nil {
			output = terminalui.PlainTerminalText(rendered)
		}
	}
	if output == "" {
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
	if len(trimmed) > lines {
		trimmed = trimmed[len(trimmed)-lines:]
	}
	return strings.TrimRight(strings.Join(trimmed, "\n"), "\n"), nil
}
