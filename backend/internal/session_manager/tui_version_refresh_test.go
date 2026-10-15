package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type viewerGate struct {
	onScreen bool
}

func (g *viewerGate) BeginInputDrain(string) (time.Time, func()) { return time.Time{}, func() {} }
func (g *viewerGate) TerminalOnScreen(string) bool               { return g.onScreen }

func TestTerminalRestartWaitsForEveryBlocker(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	mgr, _, _ := newChatManager(&recordingLauncher{})
	mgr.clock = func() time.Time { return now }
	gate := &viewerGate{}
	mgr.SetTerminalInputGate(gate)
	base := domain.SessionRecord{
		ID: "mer-1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity:    domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Hour)},
		Metadata:    domain.SessionMetadata{RuntimeHandleID: "mer-1", RuntimeLaunchID: "launch-1"},
		StaleBinary: &domain.SessionStaleBinary{Version: "2.1.300", Since: now.Add(-time.Hour)},
	}
	if !mgr.terminalRestartEligible(base, 10*time.Minute) {
		t.Fatal("an idle, unwatched, stale terminal was not eligible")
	}
	failed := now
	for name, change := range map[string]func(*domain.SessionRecord){
		"current release": func(r *domain.SessionRecord) { r.StaleBinary = nil },
		"failed switch": func(r *domain.SessionRecord) {
			r.StaleBinary = &domain.SessionStaleBinary{Version: "2.1.300", SwitchFailedAt: &failed}
		},
		"working":               func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityActive },
		"pending approval":      func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityBlocked },
		"recently active":       func(r *domain.SessionRecord) { r.Activity.LastActivityAt = now.Add(-time.Minute) },
		"untested harness":      func(r *domain.SessionRecord) { r.Harness = domain.HarnessAider },
		"orchestrator":          func(r *domain.SessionRecord) { r.Kind = domain.KindOrchestrator },
		"chat":                  func(r *domain.SessionRecord) { r.Mode = domain.SessionModeChat },
		"terminated":            func(r *domain.SessionRecord) { r.IsTerminated = true },
		"no terminal to resume": func(r *domain.SessionRecord) { r.Metadata.RuntimeHandleID = "" },
	} {
		rec := base
		change(&rec)
		if mgr.terminalRestartEligible(rec, 10*time.Minute) {
			t.Fatalf("%s: restart was allowed", name)
		}
	}
	gate.onScreen = true
	if mgr.terminalRestartEligible(base, 10*time.Minute) {
		t.Fatal("restarted a terminal shown on screen")
	}
}

type styledScreenRuntime struct {
	*fakeRuntime
	screen string
}

func (r *styledScreenRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.screen, nil
}

// A viewer leaving sends a focus change into the terminal after the idle
// report, and no new idle report follows. The rendered screen decides then.
func TestTerminalRestartAfterInputThatChangedNothing(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	mgr, _, rt := newChatManager(&recordingLauncher{})
	mgr.clock = func() time.Time { return now }
	mgr.agents = singleAgent{agent: transitionSurfaceAgent{}}
	screen := &styledScreenRuntime{fakeRuntime: rt}
	mgr.runtime = screen
	rec := domain.SessionRecord{
		ID: "mer-1", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Hour)},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "mer-1", RuntimeLaunchID: "launch-1"},
	}
	ctx := context.Background()

	if !mgr.terminalIdleForRestart(ctx, rec, now.Add(-2*time.Hour)) {
		t.Fatal("an idle report newer than the last input was not enough")
	}
	focusChange := now.Add(-30 * time.Minute)
	for _, test := range []struct {
		screen string
		want   bool
	}{
		{screen: idleTerminalOutput, want: true},
		{screen: draftTerminalOutput},
		{screen: activeTerminalOutput},
		{screen: ambiguousTerminalOutput},
	} {
		screen.screen = test.screen
		if got := mgr.terminalIdleForRestart(ctx, rec, focusChange); got != test.want {
			t.Errorf("screen %q: restart = %v, want %v", test.screen, got, test.want)
		}
	}
	screen.screen = idleTerminalOutput
	if mgr.terminalIdleForRestart(ctx, rec, now.Add(-100*time.Millisecond)) {
		t.Fatal("restarted before the latest input settled")
	}
}
