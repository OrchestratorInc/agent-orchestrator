package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

type fakeAutoPauseSettings struct{ minutes int }

func (f fakeAutoPauseSettings) Get(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{AutoPauseIdleMinutes: f.minutes}, nil
}

type fakeAutoPauser struct{ calls []sessionsvc.PauseIdleInput }

func (f *fakeAutoPauser) PauseIdle(_ context.Context, in sessionsvc.PauseIdleInput) (sessionsvc.PauseIdleOutcome, error) {
	f.calls = append(f.calls, in)
	return sessionsvc.PauseIdleOutcome{}, nil
}

func TestAutoPauseTickDoesNothingWhileOff(t *testing.T) {
	pauser := &fakeAutoPauser{}
	autoPauseTick(context.Background(), fakeAutoPauseSettings{minutes: 0}, pauser, slog.Default())
	if len(pauser.calls) != 0 {
		t.Fatalf("sweep ran with auto-pause off: %+v", pauser.calls)
	}
}

func TestAutoPauseTickSweepsWithTheConfiguredThreshold(t *testing.T) {
	pauser := &fakeAutoPauser{}
	autoPauseTick(context.Background(), fakeAutoPauseSettings{minutes: 30}, pauser, slog.Default())
	if len(pauser.calls) != 1 {
		t.Fatalf("sweeps = %d, want 1", len(pauser.calls))
	}
	if got := pauser.calls[0]; got.IdleFor != 30*time.Minute || got.Reason != domain.SessionPauseIdle || got.Project != "" {
		t.Fatalf("sweep input = %+v", got)
	}
}
