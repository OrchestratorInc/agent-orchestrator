package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/wakeguard"
)

// A message for a Chat session whose agent exited, while the session itself
// stays open, restarts the agent. The message is queued durably first, so a
// failed restart loses nothing: it stays queued until the next start, which
// keeps and delivers it (HoldChatWake). This is the hibernated-session wake
// with two differences: only trusted senders start it, and a failure keeps the
// queue instead of failing it, because senders such as worker reports retry
// with the same idempotency key and would otherwise find a failed duplicate.

// exitedAwaitingWake reports whether rec is an open, ready Chat session whose
// agent exited. Whether a controller is still live is checked separately.
func exitedAwaitingWake(rec domain.SessionRecord) bool {
	return !rec.IsTerminated && rec.HibernatedAt == nil &&
		rec.Activity.State == domain.ActivityExited &&
		domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
		rec.ProvisionState.WithDefault() == domain.SessionProvisionReady
}

// exitedWake is one background restart. done closes once the agent started and
// the queue was handed to it, or once the restart failed.
type exitedWake struct {
	done chan struct{}
	err  error
}

func finishedExitedWake(err error) *exitedWake {
	w := &exitedWake{done: make(chan struct{}), err: err}
	close(w.done)
	return w
}

func (s *Service) wakeBudgetLocked() *wakeguard.Budget {
	if s.wakeBudget == nil {
		s.wakeBudget = wakeguard.New(s.now)
	}
	return s.wakeBudget
}

// ensureExitedWake joins the restart in flight for id or starts one. It
// returns nil when id needs none: its agent runs, or it is not an exited Chat
// session. A restart the budget refuses comes back already finished, carrying
// the reason. Concurrent senders share one restart.
func (s *Service) ensureExitedWake(ctx context.Context, id domain.SessionID) *exitedWake {
	s.wakeMu.Lock()
	if w := s.exitedWakes[id]; w != nil {
		s.wakeMu.Unlock()
		return w
	}
	s.wakeMu.Unlock()
	// The message is already queued; a sender that gives up now must not keep
	// the restart from starting.
	detached := context.WithoutCancel(ctx)
	rec, err := s.requireChatSession(detached, id)
	if err != nil {
		return finishedExitedWake(err)
	}
	if !exitedAwaitingWake(rec) || s.HasLiveChatController(id) {
		return nil
	}
	s.wakeMu.Lock()
	if w := s.exitedWakes[id]; w != nil {
		s.wakeMu.Unlock()
		return w
	}
	if err := s.wakeBudgetLocked().Admit(id); err != nil {
		s.wakeMu.Unlock()
		s.log.Warn("exited chat agent not restarted for a queued message", "session", id, "err", err)
		return finishedExitedWake(err)
	}
	if s.exitedWakes == nil {
		s.exitedWakes = make(map[domain.SessionID]*exitedWake)
	}
	w := &exitedWake{done: make(chan struct{})}
	s.exitedWakes[id] = w
	s.wakeMu.Unlock()
	// The restart outlives the sender that started it: others wait on it, and
	// the queued turns need it whether or not this caller stays.
	go s.runExitedWake(detached, id, w)
	return w
}

// exitedWakeDrainAttempts and exitedWakeDrainRetry bound how hard a restart
// tries to hand the queue to the new controller before reporting it pending.
const (
	exitedWakeDrainAttempts = 3
	exitedWakeDrainRetry    = time.Second
)

func (s *Service) runExitedWake(ctx context.Context, id domain.SessionID, w *exitedWake) {
	restartErr := s.wakeSession(ctx, id, true)
	result := restartErr
	if restartErr == nil {
		if drainErr := s.drainAfterExitedWake(ctx, id); drainErr != nil {
			// The agent runs but has not taken the queue. The message stays
			// queued, and the sender is told it is still pending, not delivered.
			s.log.Warn("chat agent restarted but its queued messages were not dispatched yet", "session", id, "err", drainErr)
			captureChatFault(ctx, drainErr, "", "chat_wake", "drain_exited")
			result = fmt.Errorf("%w: %w", ports.ErrAgentWakeDeliveryPending, drainErr)
		}
	} else {
		s.log.Warn("could not restart an exited chat agent; its queued messages are kept for the next start", "session", id, "err", restartErr)
		captureChatFault(ctx, restartErr, "", "chat_wake", "wake_exited")
	}
	s.wakeMu.Lock()
	// The budget counts the restart itself; a pending drain is not a failed start.
	s.wakeBudgetLocked().Record(id, restartErr)
	w.err = result
	delete(s.exitedWakes, id)
	close(w.done)
	s.wakeMu.Unlock()
}

// drainAfterExitedWake hands the kept queue to the restarted controller,
// retrying briefly so one transient failure does not leave it waiting for
// some later drain.
func (s *Service) drainAfterExitedWake(ctx context.Context, id domain.SessionID) error {
	var err error
	for attempt := 1; attempt <= exitedWakeDrainAttempts; attempt++ {
		if err = s.DrainQueued(ctx, id); err == nil {
			return nil
		}
		if attempt < exitedWakeDrainAttempts {
			time.Sleep(exitedWakeDrainRetry)
		}
	}
	return err
}

// AwaitChatWake restarts an exited Chat session's agent if it needs it, or
// joins the restart already running, and waits for the result. It returns nil
// when nothing needed restarting or the agent restarted and took the queue,
// and an error wrapping ports.ErrAgentWakeDeliveryPending when the agent
// restarted but the queue is still waiting.
func (s *Service) AwaitChatWake(ctx context.Context, id domain.SessionID) error {
	w := s.ensureExitedWake(ctx, id)
	if w == nil {
		return nil
	}
	select {
	case <-w.done:
		return w.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HoldChatWake marks id as waking while Session Manager restarts its agent by
// any route, a manual resume included. A start marked so keeps the messages
// queued while the agent was down instead of settling them as orphaned.
func (s *Service) HoldChatWake(id domain.SessionID) (release func()) {
	s.wakeMu.Lock()
	if s.waking == nil {
		s.waking = make(map[domain.SessionID]int)
	}
	s.waking[id]++
	s.wakeMu.Unlock()
	released := false
	return func() {
		s.wakeMu.Lock()
		defer s.wakeMu.Unlock()
		if released {
			return
		}
		released = true
		s.waking[id]--
		if s.waking[id] <= 0 {
			delete(s.waking, id)
		}
	}
}

// callerStoppedWaiting reports whether the sender's own request ended. It is
// checked apart from the restart's error: a restart that timed out on its own
// is a failure even while the sender still waits.
func callerStoppedWaiting(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// queueForExitedWake accepts msg for an exited session and waits for its
// agent to restart. pending reports that the message is queued but the agent
// has not taken it yet: the sender stopped waiting, or the agent restarted and
// the queue hand-off is still outstanding. On a failed restart the message
// stays queued and the error says so. A refused append (the session came back
// meanwhile) returns ErrNotProvisioning so the caller sends through the live
// controller.
func (s *Service) queueForExitedWake(ctx context.Context, record domain.SessionRecord, msg ports.ChatUserMessage) (turn domain.ConversationTurn, pending bool, err error) {
	turn, err = s.queueWithoutController(ctx, record, msg, false)
	if err != nil {
		return domain.ConversationTurn{}, false, err
	}
	wakeErr := s.AwaitChatWake(ctx, record.ID)
	switch {
	case wakeErr == nil:
		return turn, false, nil
	case errors.Is(wakeErr, ports.ErrAgentWakeDeliveryPending), callerStoppedWaiting(ctx):
		return turn, true, nil
	default:
		return turn, false, &ports.AgentWakeError{Reason: wakeErr, Queued: true}
	}
}
