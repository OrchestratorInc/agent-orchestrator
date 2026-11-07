package lifecycle

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	if f.Runtime != ports.ProbeDead {
		return false
	}
	// Stickiness only stops time from demoting a paused agent. A conclusive
	// dead probe outranks it: the agent that would clear the pause is gone.
	if activity.State.IsSticky() {
		return true
	}
	return !hasRecentActivity(activity, timeOr(f.ObservedAt, now), window)
}

func timeOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
