package lifecycle

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// kimiChildSignal keeps a Kimi child agent's failure from ending its parent's
// turn, and reports whether it consumed the signal. Kimi runs its hooks per
// agent and names no agent in the payload, so a child's StopFailure reads
// exactly like the parent's. Kimi runs SubagentStart before each child and
// SubagentStop after one that succeeds; a child that fails reports only its
// StopFailure. A StopFailure while a child is open is therefore that child's,
// and the parent, still working, must neither go idle nor close its turn.
func (m *Manager) kimiChildSignal(id domain.SessionID, s ports.ActivitySignal) bool {
	if s.ExpectedHarness != domain.HarnessKimi {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch s.Event {
	case "subagent-start":
		m.kimiChildren[id]++
		return true
	case "subagent-stop":
		m.closeKimiChildLocked(id)
		return true
	case "stop-failure":
		if m.kimiChildren[id] == 0 {
			return false
		}
		m.closeKimiChildLocked(id)
		return true
	case "session-start", "user-prompt-submit":
		// A new turn opens with no child running, so a child whose end was
		// never reported (an Esc'd child fires neither hook) cannot swallow
		// this turn's own failure.
		delete(m.kimiChildren, id)
	}
	return false
}

func (m *Manager) closeKimiChildLocked(id domain.SessionID) {
	if m.kimiChildren[id] <= 1 {
		delete(m.kimiChildren, id)
		return
	}
	m.kimiChildren[id]--
}
