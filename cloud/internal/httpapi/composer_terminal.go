package httpapi

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// composerTerminalStore admits keystrokes into a session's agent terminal
// under its composer lease.
type composerTerminalStore interface {
	ComposerTerminalInput(ctx context.Context, orgID, sessionID, userID string) (bool, error)
}

// composerInputGrace is how long an admitted keystroke admits the ones after
// it without asking the store again. A lease change anyone can see ends it
// early, so a take-over stops input at once.
const composerInputGrace = 3 * time.Second

// composerRefusalInterval spaces out input_refused messages while someone
// keeps typing without the lease.
const composerRefusalInterval = time.Second

// composerInputGate decides whether one agent terminal connection's
// keystrokes reach the agent. Typing into the agent terminal is talking to the
// agent, so it takes the composer lease like the chat composer does; while
// someone else holds the lease the keystrokes are refused.
type composerInputGate struct {
	store        composerTerminalStore
	terminal     domain.TerminalSession
	admitUntil   atomic.Int64 // unix nanoseconds
	lastRefusal  time.Time
	stopWatching func()
}

// newComposerInputGate returns the gate for an agent terminal connection, or
// nil when no gate applies: another kind of terminal, a read-only one, a
// ticket from before tickets named their user, or a store without leases.
func (s *Server) newComposerInputGate(ctx context.Context, terminal domain.TerminalSession) *composerInputGate {
	if terminal.Kind != "agent" || terminal.UserID == "" || !terminalScope(terminal.Scopes, "terminal:operate") {
		return nil
	}
	store, ok := s.store.(composerTerminalStore)
	if !ok {
		return nil
	}
	gate := &composerInputGate{store: store, terminal: terminal, stopWatching: func() {}}
	if s.sessionLiveWaiters != nil {
		wake, cancel := s.sessionLiveWaiters.subscribe(terminal.SessionID)
		watchCtx, stop := context.WithCancel(ctx)
		gate.stopWatching = func() { stop(); cancel() }
		go func() {
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-wake:
					gate.admitUntil.Store(0)
				}
			}
		}()
	}
	return gate
}

func (g *composerInputGate) close() {
	if g != nil {
		g.stopWatching()
	}
}

// admit reports whether a keystroke may go to the agent now.
func (g *composerInputGate) admit(ctx context.Context, now time.Time) (bool, error) {
	if g == nil {
		return true, nil
	}
	if now.UnixNano() < g.admitUntil.Load() {
		return true, nil
	}
	allowed, err := g.store.ComposerTerminalInput(ctx, g.terminal.OrgID, g.terminal.SessionID, g.terminal.UserID)
	if err != nil || !allowed {
		return false, err
	}
	g.admitUntil.Store(now.Add(composerInputGrace).UnixNano())
	return true, nil
}

// shouldTellRefusal reports whether to tell the client about a refusal now,
// at most once per composerRefusalInterval.
func (g *composerInputGate) shouldTellRefusal(now time.Time) bool {
	if now.Sub(g.lastRefusal) < composerRefusalInterval {
		return false
	}
	g.lastRefusal = now
	return true
}
