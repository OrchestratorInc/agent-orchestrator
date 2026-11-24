package lifecycle

import (
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const defaultRecentActivityWindow = 60 * time.Second

func hasRecentActivity(a domain.Activity, now time.Time, window time.Duration) bool {
	switch {
	case a.State == domain.ActivityExited:
		return false
	case a.State.IsSticky():
		return true
	case a.LastActivityAt.IsZero():
		return false
	default:
		return now.Sub(a.LastActivityAt) <= window
	}
}

func runtimeClearlyDead(f ports.RuntimeFacts, activity domain.Activity, now time.Time, window time.Duration) bool {
	observedAt := timeOr(f.ObservedAt, now)
	return f.Runtime == ports.ProbeDead && !hasRecentActivity(activity, observedAt, window)
}

func timeOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
