package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type startupCueStore interface {
	SelectStartupCue(context.Context, domain.ProjectID) (domain.Cue, bool, error)
	ClaimStartupCue(context.Context, domain.SessionID, domain.StartupCueRun) (bool, error)
	BeginStartupCue(context.Context, domain.SessionID, domain.StartupCueRun) (bool, error)
	FinishStartupCue(context.Context, domain.SessionID, domain.StartupCueRun) error
	EnqueueStartupCueMessage(context.Context, domain.SessionID, string, string) (bool, error)
	ListStartupCueMessages(context.Context, domain.SessionID) ([]domain.StartupCueMessage, error)
	MarkStartupCueMessageDelivered(context.Context, int64) error
}

// prepareStartupCue pins the definition before any controller can accept input.
func (m *Manager) prepareStartupCue(ctx context.Context, id domain.SessionID, project domain.ProjectID) error {
	store, ok := m.store.(startupCueStore)
	if !ok || project == "" {
		return nil
	}
	cue, found, err := store.SelectStartupCue(ctx, project)
	if err != nil || !found {
		return err
	}
	timeout := cue.StartupTimeoutSeconds
	if timeout == 0 {
		timeout = 600
	}
	_, err = store.ClaimStartupCue(ctx, id, domain.StartupCueRun{
		CueID: cue.ID, Name: cue.Name, Command: cue.Command, Shell: cue.StartupShell,
		TimeoutSeconds: timeout, State: "pending", StartedAt: m.clock(),
	})
	return err
}

// startStartupCue waits for command completion inside a daemon-owned worker.
// Session creation stays responsive; provider delivery is gated durably.
func (m *Manager) startStartupCue(id domain.SessionID, project domain.ProjectRecord, workspace string, release func(context.Context) error) {
	m.runInBackground(func() {
		ctx := m.backgroundContext
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil || !found || !rec.StartupCue.HoldsInput() {
			return
		}
		store, ok := m.store.(startupCueStore)
		if !ok {
			return
		}
		run := *rec.StartupCue
		run.State = "running"
		run.StartedAt = m.clock()
		began, err := store.BeginStartupCue(ctx, id, run)
		if err != nil {
			m.logger.Error("startup cue: record execution", "sessionID", id, "error", err)
			return
		}
		if !began {
			return
		}
		commandCtx, cancel := context.WithTimeout(ctx, time.Duration(run.TimeoutSeconds)*time.Second)
		// Kill can arrive while a command is running, independently of startup's API request.
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-commandCtx.Done():
					return
				case <-ticker.C:
					current, exists, readErr := m.store.GetSession(commandCtx, id)
					if readErr == nil && (!exists || current.IsTerminated) {
						cancel()
						return
					}
				}
			}
		}()
		result := runWorkspaceCommand(commandCtx, run.Command, run.Shell, workspace, m.runtimeEnv(id, domain.ProjectID(project.ID), "", project.Config.Env), 64<<10)
		output, code, commandErr := result.Output, result.ExitCode, result.Err
		close(done)
		deadlineErr := commandCtx.Err()
		cancel()
		now := m.clock()
		run.CompletedAt, run.Output, run.ExitCode = &now, output, code
		run.State = "succeeded"
		if commandErr != nil {
			run.State, run.Error = "failed", commandErr.Error()
			if errors.Is(deadlineErr, context.DeadlineExceeded) {
				run.Error = fmt.Sprintf("Startup cue timed out after %d seconds", run.TimeoutSeconds)
			}
			if errors.Is(deadlineErr, context.Canceled) {
				run.State, run.Error = "cancelled", "Startup cue was interrupted"
			}
		}
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		isTUI := domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI
		if isTUI {
			releaseDelivery := m.lockStartupDelivery(id)
			defer releaseDelivery()
			run.DeliveryHeld = true
		}
		if err := store.FinishStartupCue(persistCtx, id, run); err != nil {
			m.logger.Error("startup cue: persist result", "sessionID", id, "error", err)
			return
		}
		if isTUI {
			defer func() {
				clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer clearCancel()
				run.DeliveryHeld = false
				if err := store.FinishStartupCue(clearCtx, id, run); err != nil {
					m.logger.Error("startup cue: release terminal input", "sessionID", id, "error", err)
				}
			}()
		}
		current, exists, err := m.store.GetSession(ctx, id)
		if err != nil || !exists || current.IsTerminated || ctx.Err() != nil {
			return
		}
		if err := release(ctx); err != nil {
			m.logger.Warn("startup cue: resume delivery", "sessionID", id, "error", err)
		}
	})
}

func (m *Manager) drainStartupCueMessages(ctx context.Context, id domain.SessionID) error {
	store, ok := m.store.(startupCueStore)
	if !ok {
		return nil
	}
	messages, err := store.ListStartupCueMessages(ctx, id)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if !found || rec.IsTerminated {
			return ErrTerminated
		}
		if rec.StartupCue != nil && rec.StartupCue.DeliveryHeld {
			message, err := m.prepareOutboundMessage(ctx, id, msg.Message)
			if err != nil {
				return err
			}
			// Startup owns delivery admission while raw terminal input stays held.
			outcome, err := m.messenger.DeliverUnderMutation(ctx, id, message)
			if err != nil {
				return err
			}
			if outcome != sessionguard.Sent {
				return fmt.Errorf("startup cue: queued delivery suppressed: %s", outcome.String())
			}
		} else if err := m.send(ctx, id, msg.Message, msg.ClientMessageID, true); err != nil {
			return err
		}
		if err := store.MarkStartupCueMessageDelivered(ctx, msg.ID); err != nil {
			return err
		}
	}
	return nil
}

// recoverStartupCues releases a hold that belonged to the previous daemon.
// The command outcome is uncertain, so recovery never reruns it.
func (m *Manager) recoverStartupCues(ctx context.Context, records []domain.SessionRecord) error {
	store, ok := m.store.(startupCueStore)
	if !ok {
		return nil
	}
	for i := range records {
		rec := &records[i]
		if !rec.StartupCue.HoldsInput() {
			continue
		}
		run := *rec.StartupCue
		now := m.clock()
		run.State, run.Error, run.CompletedAt = "interrupted", "AO restarted while the startup cue was running; it was not rerun", &now
		run.DeliveryHeld = false
		if err := store.FinishStartupCue(ctx, rec.ID, run); err != nil {
			return err
		}
		rec.StartupCue = &run
		// A committed controller can be reattached normally; its cue failure is
		// separate from provider startup and must not become a failed spawn.
		if rec.Metadata.WorkspacePath != "" && (rec.Metadata.RuntimeHandleID != "" || rec.Metadata.ProviderConversationID != "") {
			if _, err := m.setProvisionState(ctx, rec.ID, domain.SessionProvisionReady, ""); err != nil {
				return err
			}
			rec.ProvisionState = domain.SessionProvisionReady
		}
	}
	return nil
}

func (m *Manager) lockStartupDelivery(id domain.SessionID) func() {
	value, _ := m.startupDeliveryLocks.LoadOrStore(id, &sync.Mutex{})
	lock, ok := value.(*sync.Mutex)
	if !ok {
		panic("startup delivery lock has an invalid type")
	}
	lock.Lock()
	return lock.Unlock
}
