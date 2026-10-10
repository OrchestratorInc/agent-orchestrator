package chat

import (
	"context"
	"errors"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AcquireAccountRoutingPause fences prompt admission and queue dispatch without
// interrupting work or replacing the native conversation.
func (c *Controller) AcquireAccountRoutingPause(ctx context.Context) (func(), error) {
	if !c.sendMu.TryLock() {
		return nil, ports.ErrProviderAccountBusy
	}
	release := func() { c.sendMu.Unlock() }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	c.mu.Lock()
	busy := c.pendingTurnID != "" || c.handoff != controllerHandoffNone
	c.mu.Unlock()
	if busy {
		release()
		return nil, ports.ErrProviderAccountBusy
	}
	running, err := c.store.ListVisibleRunningTurnProviderIDs(ctx, c.conversation.ID)
	if err != nil {
		release()
		return nil, err
	}
	if len(running) > 0 {
		release()
		return nil, ports.ErrProviderAccountBusy
	}
	_, err = c.store.NextQueuedTurn(ctx, c.conversation.ID)
	if !errors.Is(err, domain.ErrNoQueuedTurn) {
		release()
		if err != nil {
			return nil, err
		}
		return nil, ports.ErrProviderAccountBusy
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}

// AcquireAccountRoutingPause fences the live worker controller while its routing changes.
func (s *Service) AcquireAccountRoutingPause(ctx context.Context, id domain.SessionID) (func(), error) {
	controller, err := s.Controller(id)
	if err != nil {
		return nil, ports.ErrProviderAccountBusy
	}
	return controller.AcquireAccountRoutingPause(ctx)
}
