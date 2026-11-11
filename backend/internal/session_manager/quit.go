package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
				_, stopErr = m.exitAgent(ctx, rec.ID, func() error {
					if canSleepStoppedChat(rec) && m.settleAbandonedChatWork(ctx, rec.ID) {
						return m.sleepStoppedChat(ctx, rec)
					}
					return nil
				})
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

func canSleepStoppedChat(rec domain.SessionRecord) bool {
	return domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
		rec.Kind != domain.KindOrchestrator &&
		rec.ProvisionState.WithDefault() == domain.SessionProvisionReady &&
		strings.TrimSpace(rec.Metadata.ProviderConversationID) != ""
}

func (m *Manager) sleepStoppedChat(ctx context.Context, rec domain.SessionRecord) error {
	store, ok := m.store.(interface {
		SetSessionHibernated(context.Context, domain.SessionID, int64, *time.Time) (bool, error)
	})
	if !ok {
		return fmt.Errorf("sleep chat %s: hibernation store unavailable", rec.ID)
	}
	if err := m.lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid:                true,
		State:                domain.ActivityIdle,
		Event:                "chat.controller.stopped",
		ControllerGeneration: strings.TrimSpace(rec.Metadata.ControllerGeneration),
		Timestamp:            m.clock(),
	}); err != nil {
		return fmt.Errorf("sleep chat %s: %w", rec.ID, err)
	}
	for range 3 {
		current, found, err := m.store.GetSession(ctx, rec.ID)
		if err != nil || !found || current.IsTerminated || current.HibernatedAt != nil {
			return err
		}
		at := m.clock()
		if applied, err := store.SetSessionHibernated(ctx, rec.ID, current.Revision, &at); err != nil || applied {
			return err
		}
	}
	return fmt.Errorf("sleep chat %s: session changed concurrently", rec.ID)
}

func (m *Manager) settleAbandonedChatWork(ctx context.Context, id domain.SessionID) bool {
	settler, ok := m.chat.(interface {
		SettleAbandonedWork(context.Context, domain.SessionID) error
	})
	if !ok {
		return false
	}
	if err := settler.SettleAbandonedWork(ctx, id); err != nil {
		m.logger.Warn("chat stays stopped: could not settle the work its host left open", "sessionID", id, "error", err)
		return false
	}
	return true
}
