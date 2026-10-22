package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A sticky state only guards against time-based demotion; it must not outrank
// a conclusive dead probe (#6726). Failed and alive probes never prove death.
func TestRuntimeClearlyDead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-10 * time.Second)
	stale := now.Add(-2 * time.Minute)
	for _, tc := range []struct {
		name  string
		state domain.ActivityState
		last  time.Time
		probe ports.ProbeResult
		want  bool
	}{
		{name: "blocked recent dead", state: domain.ActivityBlocked, last: recent, probe: ports.ProbeDead, want: true},
		{name: "blocked stale dead", state: domain.ActivityBlocked, last: stale, probe: ports.ProbeDead, want: true},
		{name: "blocked failed", state: domain.ActivityBlocked, last: stale, probe: ports.ProbeFailed, want: false},
		{name: "blocked alive", state: domain.ActivityBlocked, last: stale, probe: ports.ProbeAlive, want: false},
		{name: "waiting_input recent dead", state: domain.ActivityWaitingInput, last: recent, probe: ports.ProbeDead, want: true},
		{name: "waiting_input stale dead", state: domain.ActivityWaitingInput, last: stale, probe: ports.ProbeDead, want: true},
		{name: "waiting_input failed", state: domain.ActivityWaitingInput, last: stale, probe: ports.ProbeFailed, want: false},
		{name: "waiting_input alive", state: domain.ActivityWaitingInput, last: stale, probe: ports.ProbeAlive, want: false},
		{name: "active recent dead", state: domain.ActivityActive, last: recent, probe: ports.ProbeDead, want: false},
		{name: "active stale dead", state: domain.ActivityActive, last: stale, probe: ports.ProbeDead, want: true},
		{name: "active stale failed", state: domain.ActivityActive, last: stale, probe: ports.ProbeFailed, want: false},
		{name: "active stale alive", state: domain.ActivityActive, last: stale, probe: ports.ProbeAlive, want: false},
		{name: "idle recent dead", state: domain.ActivityIdle, last: recent, probe: ports.ProbeDead, want: false},
		{name: "idle stale dead", state: domain.ActivityIdle, last: stale, probe: ports.ProbeDead, want: true},
		{name: "idle stale failed", state: domain.ActivityIdle, last: stale, probe: ports.ProbeFailed, want: false},
		{name: "idle stale alive", state: domain.ActivityIdle, last: stale, probe: ports.ProbeAlive, want: false},
		{name: "exited recent dead", state: domain.ActivityExited, last: recent, probe: ports.ProbeDead, want: true},
		{name: "exited recent failed", state: domain.ActivityExited, last: recent, probe: ports.ProbeFailed, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			activity := domain.Activity{State: tc.state, LastActivityAt: tc.last}
			facts := ports.RuntimeFacts{Runtime: tc.probe, ObservedAt: now}
			if got := runtimeClearlyDead(facts, activity, now, defaultRecentActivityWindow); got != tc.want {
				t.Fatalf("runtimeClearlyDead = %v, want %v", got, tc.want)
			}
		})
	}
}
