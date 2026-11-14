package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// StopAgentsForQuit releases processes without terminating sessions or removing worktrees.
func (m *Manager) StopAgentsForQuit(ctx context.Context) error {
	records, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("stop agents for quit: %w", err)
	}
	errs := make(chan error, len(records))
	var workers sync.WaitGroup
	for _, rec := range records {
		if rec.IsTerminated || rec.IsTaskPreparation {
			continue
		}
		workers.Go(func() {
			release, shellErr := m.beginShellTerminalTeardown(ctx, rec.ID)
			if release != nil {
				defer release()
			}
			reviewErr := m.teardownReviewerTerminal(ctx, rec.ID)
			var stopErr error
			if rec.Activity.State == domain.ActivityExited {
				// A disconnected controller can still have a surviving provider host.
				if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat || rec.Metadata.RuntimeHandleID != "" {
					stopErr = m.releaseTerminalRuntime(ctx, rec)
					if stopErr == nil {
						stopErr = m.stopAgentController(ctx, rec)
					}
				}
			} else {
				_, stopErr = m.ExitAgent(ctx, rec.ID)
				if errors.Is(stopErr, ErrAgentExited) {
					stopErr = nil
				}
			}
			if err := errors.Join(shellErr, reviewErr, stopErr); err != nil {
				errs <- fmt.Errorf("stop agent %s for quit: %w", rec.ID, err)
			}
		})
	}
	workers.Wait()
	close(errs)
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
