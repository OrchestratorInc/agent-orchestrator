package session

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func TestPauseIdleSelectsOnlyLongIdleWorkerAgents(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	pausedAt := now.Add(-time.Hour)
	st := newFakeStore()
	tui := domain.SessionMetadata{RuntimeHandleID: "tmux-x"}
	for _, rec := range []domain.SessionRecord{
		{ID: "idle-old", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-45 * time.Minute)}},
		{ID: "idle-fresh", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-5 * time.Minute)}},
		{ID: "busy", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-45 * time.Minute)}},
		{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Metadata: tui, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-45 * time.Minute)}},
		{ID: "already", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, PausedAt: &pausedAt, Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: now.Add(-45 * time.Minute)}},
		{ID: "dead", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, IsTerminated: true, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-45 * time.Minute)}},
		{ID: "no-runtime", ProjectID: "p", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-45 * time.Minute)}},
		{ID: "other-project", ProjectID: "q", Kind: domain.KindWorker, Metadata: tui, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-45 * time.Minute)}},
	} {
		st.sessions[rec.ID] = rec
	}
	fc := &fakeCommander{restoreResult: sessionmanager.RestoreResult{Session: domain.SessionRecord{ID: "x"}}}
	svc := &Service{manager: fc, store: st, clock: func() time.Time { return now }}

	out, err := svc.PauseIdle(context.Background(), PauseIdleInput{Project: "p", IdleFor: 30 * time.Minute, Reason: domain.SessionPauseIdle})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out.Paused, func(i, j int) bool { return out.Paused[i] < out.Paused[j] })
	if len(out.Paused) != 1 || out.Paused[0] != "idle-old" {
		t.Fatalf("paused = %v, want only idle-old", out.Paused)
	}
	if len(fc.exited) != 1 || fc.exited[0] != "idle-old" {
		t.Fatalf("exit calls = %v", fc.exited)
	}
	if len(out.Failed) != 0 {
		t.Fatalf("failed = %v", out.Failed)
	}
}

func TestPauseIdleZeroThresholdPausesEveryIdleAgentAndReportsFailures(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	tui := domain.SessionMetadata{RuntimeHandleID: "tmux-x"}
	st.sessions["a"] = domain.SessionRecord{ID: "a", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}
	st.sessions["b"] = domain.SessionRecord{ID: "b", ProjectID: "p", Kind: domain.KindWorker, Metadata: tui, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}
	fc := &fakeCommander{restoreErr: sessionmanager.ErrAgentPauseDrainBlocked}
	svc := &Service{manager: fc, store: st, clock: func() time.Time { return now }}

	out, err := svc.PauseIdle(context.Background(), PauseIdleInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Paused) != 0 || len(out.Failed) != 2 {
		t.Fatalf("paused=%v failed=%v; a refused pause must be reported, not abort the sweep", out.Paused, out.Failed)
	}
	if len(fc.exited) != 2 {
		t.Fatalf("exit calls = %v, want both idle agents attempted", fc.exited)
	}
}
