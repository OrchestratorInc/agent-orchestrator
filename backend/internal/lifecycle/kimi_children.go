package lifecycle

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A StopFailure attributed to an open child may really be the parent's: a
// child that is cancelled or times out reports neither SubagentStop nor
// StopFailure, so its count never drops. The rendered screen settles which
// agent failed, so the activity observer rereads it during this window instead
// of waiting for the session to go stale. The settle delay lets Kimi redraw.
const (
	kimiChildFailureRecheckSettle = 5 * time.Second
	kimiChildFailureRecheckWindow = 2 * time.Minute
)

// kimiChildState is one Kimi session's open child agents, scoped to the
// runtime launch (or Chat controller) that counted them.
type kimiChildState struct {
	owner string
	open  int
	// childFailureAt is when the last StopFailure was attributed to a child.
	childFailureAt time.Time
}

// kimiChildOutcome reports what kimiChildSignalLocked did with a signal.
type kimiChildOutcome struct {
	// consumed: the signal was child bookkeeping; the parent is unchanged.
	consumed bool
	// endsFailedTurn: the screen proved the parent back at its prompt after a
	// StopFailure was attributed to a child, so that failure ended the
	// parent's turn and the open terminal turn closes unanswered.
	endsFailedTurn bool
}

// kimiChildSignalLocked keeps a Kimi child agent's failure from ending its
// parent's turn. Kimi runs its hooks per agent and names no agent in the
// payload, so a child's StopFailure reads exactly like the parent's. Kimi runs
// SubagentStart before each child and SubagentStop after one that succeeds; a
// child that fails reports only its StopFailure. A StopFailure while a child
// is open is therefore taken for that child's, and the parent, still working,
// must neither go idle nor close its turn.
//
// The caller holds m.mu and has already passed every ownership fence, so a
// superseded runtime's hooks never change the count. A count left by an
// earlier launch or controller is dropped as soon as the current owner
// reports, so it cannot swallow the new runtime's own failure.
func (m *Manager) kimiChildSignalLocked(id domain.SessionID, harness domain.AgentHarness, s ports.ActivitySignal, now time.Time) kimiChildOutcome {
	if harness != domain.HarnessKimi {
		return kimiChildOutcome{}
	}
	owner := s.LaunchID + "\x00" + s.ControllerGeneration
	st, ok := m.kimiChildren[id]
	if ok && st.owner != owner {
		delete(m.kimiChildren, id)
		st, ok = kimiChildState{}, false
	}
	st.owner = owner
	switch s.Event {
	case "subagent-start":
		st.open++
		m.kimiChildren[id] = st
		return kimiChildOutcome{consumed: true}
	case "subagent-stop":
		if st.open > 0 {
			st.open--
		}
		m.storeKimiChildLocked(id, st)
		return kimiChildOutcome{consumed: true}
	case "stop-failure":
		if st.open == 0 {
			// The parent's own failure ends the turn, and with it any
			// pending recheck.
			delete(m.kimiChildren, id)
			return kimiChildOutcome{}
		}
		st.open--
		st.childFailureAt = timeOr(s.Timestamp, now)
		m.storeKimiChildLocked(id, st)
		return kimiChildOutcome{consumed: true}
	case "session-start", "user-prompt-submit":
		// A new turn opens with no child running, so a child whose end was
		// never reported (an Esc'd or timed-out child fires neither hook)
		// cannot swallow this turn's own failure. Stop does not reset the
		// count: a child's own Stop arrives before its SubagentStop, while
		// its siblings may still run.
		delete(m.kimiChildren, id)
	case "terminal-idle":
		if ok && !st.childFailureAt.IsZero() {
			delete(m.kimiChildren, id)
			return kimiChildOutcome{endsFailedTurn: true}
		}
	}
	return kimiChildOutcome{}
}

func (m *Manager) storeKimiChildLocked(id domain.SessionID, st kimiChildState) {
	if st.open == 0 && st.childFailureAt.IsZero() {
		delete(m.kimiChildren, id)
		return
	}
	m.kimiChildren[id] = st
}

// TerminalActivityRecheckDue reports whether the activity observer should read
// a session's screen now even though its activity is not yet stale: a Kimi
// StopFailure was attributed to a child, and the screen can tell whether the
// parent is still working or its own turn failed.
//
// The window bounds only the early reads. The failure itself is kept until a
// new turn, an owner change, the parent's own failure, termination, or the
// screen read that proves the parent idle, so a later ordinary stale read
// still closes the failed turn.
func (m *Manager) TerminalActivityRecheckDue(id domain.SessionID, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.kimiChildren[id]
	if !ok || st.childFailureAt.IsZero() {
		return false
	}
	since := now.Sub(st.childFailureAt)
	return since >= kimiChildFailureRecheckSettle && since <= kimiChildFailureRecheckWindow
}
