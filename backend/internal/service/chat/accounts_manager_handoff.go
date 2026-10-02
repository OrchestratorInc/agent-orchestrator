package chat

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ArmAccountsManagerHandoff pauses queue admission without discarding queued turns.
func (s *Service) ArmAccountsManagerHandoff(ctx context.Context, id domain.SessionID, fresh bool) error {
	c, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.armAccountHandoff(ctx, fresh)
}

func (c *Controller) armAccountHandoff(ctx context.Context, fresh bool) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handoff != controllerHandoffNone && c.handoff != controllerHandoffAccount {
		return ErrControllerHandoff
	}
	if fresh {
		if _, err := c.store.NextQueuedTurn(ctx, c.conversation.ID); !errors.Is(err, domain.ErrNoQueuedTurn) {
			if err != nil {
				return err
			}
			return ErrTurnRunning
		}
	}
	c.handoff = controllerHandoffAccount
	return nil
}

// PrepareAccountsManagerHandoff applies the user's drain or interrupt policy.
func (s *Service) PrepareAccountsManagerHandoff(ctx context.Context, id domain.SessionID, policy domain.SessionInterfaceTransitionPolicy) error {
	c, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.prepareAccountHandoff(ctx, policy)
}

func (c *Controller) prepareAccountHandoff(ctx context.Context, policy domain.SessionInterfaceTransitionPolicy) error {
	if !policy.Valid() {
		return domain.ErrAccountsManagerSwitchConflict
	}
	if err := c.armAccountHandoff(ctx, false); err != nil {
		return err
	}
	if policy == domain.SessionInterfaceTransitionInterrupt {
		return c.interruptForHandoff(ctx)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		c.sendMu.Lock()
		c.mu.Lock()
		busy := c.pendingTurnID != ""
		c.mu.Unlock()
		c.sendMu.Unlock()
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopped:
			return nil
		case <-ticker.C:
		}
	}
}

// AbortAccountsManagerHandoff releases only the account handoff and resumes its queue.
func (s *Service) AbortAccountsManagerHandoff(id domain.SessionID) {
	if c, err := s.Controller(id); err == nil {
		c.releaseAccountHandoff()
	}
}

func (c *Controller) releaseAccountHandoff() {
	c.sendMu.Lock()
	c.mu.Lock()
	resume := c.handoff == controllerHandoffAccount
	if resume {
		c.handoff = controllerHandoffNone
	}
	c.mu.Unlock()
	c.sendMu.Unlock()
	if resume {
		c.resumeAccountQueue()
	}
}

func (c *Controller) resumeAccountQueue() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		select {
		case <-c.stopped:
			return
		default:
			_ = c.drain(ctx)
		}
	}()
}

// AcknowledgeAccountsManagerSwitch commits under the same lock that admits provider work.
func (s *Service) AcknowledgeAccountsManagerSwitch(ctx context.Context, id domain.SessionID, generation string, commit func(context.Context) error) error {
	c, err := s.Controller(id)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	ready := c.generation == generation && c.state == ports.ChatControllerReady && c.handoff == controllerHandoffAccount
	if !ready {
		c.mu.Unlock()
		return domain.ErrAccountsManagerSwitchConflict
	}
	if err := commit(ctx); err != nil {
		c.mu.Unlock()
		return err
	}
	c.handoff = controllerHandoffNone
	c.mu.Unlock()
	c.resumeAccountQueue()
	return nil
}
