//go:build windows

package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func (m *codexAccountManager) prepareLoginTerminalForCommit(ctx context.Context, handleID string) error {
	handleID = strings.TrimSpace(handleID)
	if m.terminal == nil || handleID == "" {
		return nil
	}
	alive, probeErr := m.terminal.IsShellTerminalChildAlive(ctx, handleID)
	if probeErr == nil && alive {
		return errors.New("Codex login command is still running")
	}
	closeErr := m.terminal.CloseShellTerminal(context.WithoutCancel(ctx), handleID)
	if closeErr == nil {
		return nil
	}
	var apiErr *apierr.Error
	if errors.As(closeErr, &apiErr) && apiErr.Kind == apierr.KindNotFound {
		return nil
	}
	if probeErr != nil {
		return errors.Join(probeErr, closeErr)
	}
	return closeErr
}
