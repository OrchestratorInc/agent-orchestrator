package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// wakeRetryAfterFailure keeps a failed restart from being retried by every
// following message, such as a worker report the coordinator retries.
const wakeRetryAfterFailure = time.Minute

// wakeExitedAgent restarts id's agent with resume-agent when it has exited
// (desktop quit, crash, exit-agent), so a message from the user or another AO
// session reaches it as if it were idle. A running agent is left alone and a
// terminated session keeps refusing. Concurrent senders share one resume.
func (m *Manager) wakeExitedAgent(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok || rec.IsTerminated || rec.Activity.State != domain.ActivityExited {
		return nil
	}
	m.wakeFailedMu.Lock()
	failedAt, failed := m.wakeFailedAt[id]
	m.wakeFailedMu.Unlock()
	if failed && m.clock().Sub(failedAt) < wakeRetryAfterFailure {
		return fmt.Errorf("%w: the last restart failed less than a minute ago", ErrAgentExited)
	}
	for {
		_, err = m.ResumeAgentWithMode(ctx, id)
		if !errors.Is(err, ErrResumeInProgress) {
			break
		}
		// Another sender (or the user's Resume) is restarting it; wait for that.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	m.wakeFailedMu.Lock()
	if err != nil && !errors.Is(err, ErrAgentNotExited) {
		if m.wakeFailedAt == nil {
			m.wakeFailedAt = make(map[domain.SessionID]time.Time)
		}
		m.wakeFailedAt[id] = m.clock()
		m.wakeFailedMu.Unlock()
		return fmt.Errorf("agent exited and could not be restarted: %w", err)
	}
	delete(m.wakeFailedAt, id)
	m.wakeFailedMu.Unlock()
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return nil
	}
	// A restarted terminal agent needs a moment before it can take input.
	return m.WaitForMessageDeliveryReady(ctx, id)
}
