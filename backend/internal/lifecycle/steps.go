package lifecycle

import (
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const stepRingSize = 20

func (m *Manager) recordStepLocked(id domain.SessionID, s ports.ActivitySignal, now time.Time) {
	if s.ToolName == "" {
		return
	}
	switch {
	case s.Event == "pre-tool-use":
		m.steps[id] = append(m.steps[id], domain.SessionStep{
			ToolUseID: s.ToolUseID,
			Tool:      s.ToolName,
			StartedAt: timeOr(s.Timestamp, now),
		})
		if steps := m.steps[id]; len(steps) > stepRingSize {
			m.steps[id] = steps[len(steps)-stepRingSize:]
		}
	case isPostToolUseEvent(s.Event):
		if s.ToolUseID == "" {
			return
		}
		steps := m.steps[id]
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i].ToolUseID != s.ToolUseID || !steps[i].EndedAt.IsZero() {
				continue
			}
			steps[i].EndedAt = timeOr(s.Timestamp, now)
			steps[i].Failed = s.Event != "post-tool-use"
			return
		}
	}
}

func (m *Manager) stepsSnapshotLocked(id domain.SessionID) []domain.SessionStep {
	return slices.Clone(m.steps[id])
}

func (m *Manager) restoreStepsLocked(id domain.SessionID, snapshot []domain.SessionStep) {
	if snapshot == nil {
		delete(m.steps, id)
		return
	}
	m.steps[id] = snapshot
}

func (m *Manager) Steps(id domain.SessionID) []domain.SessionStep {
	m.mu.Lock()
	defer m.mu.Unlock()
	steps := m.steps[id]
	out := make([]domain.SessionStep, len(steps))
	copy(out, steps)
	return out
}
