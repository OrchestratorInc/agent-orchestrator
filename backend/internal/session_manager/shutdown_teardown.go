package sessionmanager

import (
	"context"
	"errors"
	"fmt"
)

// StopAllSessionProcesses conclusively stops every live session's runtime
// process: a terminal session loses its tmux pane or pty host (the runtime's
// Destroy reaps the pane's background children, so claude/codex workers and
// their dev servers die with it), and a chat session loses its provider host.
// It is the process-teardown half of an explicit full stop (`ao stop`, issue
// #35) and is wired to the shutdown path that an explicit teardown request
// marks. Ordinary daemon shutdown never calls it: sessions survive a daemon
// restart and the next boot's Reconcile adopts them, so only a caller that
// asked to end the children gets them ended.
//
// Victim identification is by session records alone — every handle handed to
// the runtime comes from the store — so tmux on any other socket (the user's
// own server, another AO install's data dir) is never touched. Session rows,
// worktrees, and user-opened shell terminals are left alone: sessions stay
// resumable, and a shell keeps living on the tmux server it was opened on (the
// server exits by itself once its last session ends).
//
// Unlike Manager.Kill this stops no workspace, runs no cleanup commands, and
// records no termination: the only durable effect is the dead runtime a later
// read reconciles. Idempotent — an already-gone runtime stops conclusively.
func (m *Manager) StopAllSessionProcesses(ctx context.Context) error {
	// The caller's context may already be cancelled (the daemon cancels its
	// context before this runs); teardown must complete past that point, and
	// each stop carries its own bounded context.
	recs, err := m.store.ListAllSessions(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("list sessions for shutdown teardown: %w", err)
	}
	var errs []error
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if err := m.stopSourceControllerConclusive(rec); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", rec.ID, err))
		}
	}
	return errors.Join(errs...)
}
