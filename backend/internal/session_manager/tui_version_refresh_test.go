package sessionmanager

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type viewerGate struct {
	viewers int
}

func (g *viewerGate) BeginInputDrain(string) (time.Time, func()) { return time.Time{}, func() {} }
func (g *viewerGate) AttachedClients(string) int                 { return g.viewers }

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
	gate.viewers = 1
	if mgr.terminalRestartEligible(base, 10*time.Minute) {
		t.Fatal("restarted a terminal someone is watching")
	}
}
