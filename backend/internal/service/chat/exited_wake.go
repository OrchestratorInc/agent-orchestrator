package chat

import (
	"context"
	"errors"

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
	rec, err := s.requireChatSession(ctx, id)
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
	go s.runExitedWake(context.WithoutCancel(ctx), id, w)
	return w
}

func (s *Service) runExitedWake(ctx context.Context, id domain.SessionID, w *exitedWake) {
	err := s.wakeSession(ctx, id, true)
	if err == nil {
		if drainErr := s.DrainQueued(ctx, id); drainErr != nil {
			// The agent runs; the queued turn stays queued and the controller's
			// next drain delivers it.
			s.log.Warn("chat agent restarted but its queued messages were not dispatched yet", "session", id, "err", drainErr)
			captureChatFault(ctx, drainErr, "", "chat_wake", "drain_exited")
		}
	} else {
		s.log.Warn("could not restart an exited chat agent; its queued messages are kept for the next start", "session", id, "err", err)
		captureChatFault(ctx, err, "", "chat_wake", "wake_exited")
	}
	s.wakeMu.Lock()
	s.wakeBudgetLocked().Record(id, err)
	w.err = err
	delete(s.exitedWakes, id)
	close(w.done)
	s.wakeMu.Unlock()
}

// AwaitChatWake restarts an exited Chat session's agent if it needs it, or
// joins the restart already running, and waits for the result. It returns nil
// when nothing needed restarting.
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

// queueForExitedWake accepts msg for an exited session and waits for its
// agent to restart. On a failed restart the message stays queued and the
// error says so. A refused append (the session came back meanwhile) returns
// ErrNotProvisioning so the caller sends through the live controller.
func (s *Service) queueForExitedWake(ctx context.Context, record domain.SessionRecord, msg ports.ChatUserMessage) (domain.ConversationTurn, error) {
	turn, err := s.queueWithoutController(ctx, record, msg, false)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if wakeErr := s.AwaitChatWake(ctx, record.ID); wakeErr != nil {
		if errors.Is(wakeErr, context.Canceled) || errors.Is(wakeErr, context.DeadlineExceeded) {
			// The restart continues in the background; only this caller stopped waiting.
			return turn, nil
		}
		return turn, &ports.AgentWakeError{Reason: wakeErr, Queued: true}
	}
	return turn, nil
}
