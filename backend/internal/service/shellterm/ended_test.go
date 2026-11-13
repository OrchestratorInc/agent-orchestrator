package shellterm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (f *fakeShellTerminalStore) MarkShellTerminalEnded(_ context.Context, handleID string, endedAt time.Time) (bool, error) {
	if f.markEndedErr != nil {
		return false, f.markEndedErr
	}
	for i, rec := range f.records {
		if rec.HandleID == handleID && rec.EndedAt == nil {
			at := endedAt
			f.records[i].EndedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeShellTerminalStore) UpdateShellTerminalAgentHarness(_ context.Context, handleID string, harness domain.AgentHarness) error {
	for i, rec := range f.records {
		if rec.HandleID == handleID {
			f.records[i].LastAgentHarness = harness
		}
	}
	return nil
}

func (f *fakeShellTerminalStore) ReplaceShellTerminal(_ context.Context, oldHandleID string, rec ShellTerminalRecord) (bool, error) {
	for i, old := range f.records {
		if old.HandleID == oldHandleID {
			f.records[i] = rec
			return true, nil
		}
	}
	return false, nil
}

// sessionShell is a durable user shell in session-1 from a previous launch.
func sessionShell(handleID string) ShellTerminalRecord {
	return ShellTerminalRecord{
		HandleID: handleID, SessionID: "session-1", ProjectID: "project-1", AppRunID: "previous",
		WorkingDir: "/worktree", Title: "Build", TitleExplicit: true,
		CreatedAt: time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC),
	}
}

func endedSessionShell(handleID string, harness domain.AgentHarness) ShellTerminalRecord {
	rec := sessionShell(handleID)
	at := time.Date(2026, 11, 10, 20, 44, 0, 0, time.UTC)
	rec.EndedAt = &at
	rec.LastAgentHarness = harness
	return rec
}

func reopenTestService(t *testing.T, rt *fakeShellRuntime, st *fakeShellTerminalStore, workspace string) *Service {
	t.Helper()
	projects := &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"project-1": t.TempDir()}}
	sessions := &fakeSessionWorkspaceLocator{sessions: map[domain.SessionID]fakeSessionWorkspace{
		"session-1": {workspacePath: workspace, projectID: "project-1"},
	}}
	svc := newTestServiceWithSessions(rt, st, projects, sessions)
	svc.dataDir = t.TempDir()
	return svc
}

// The reported loss: after a reboot no native PTY host exists, so the child is
// conclusively gone. A session's side terminal must stay listed as ended, not
// silently disappear.
func TestRuntimeLossKeepsSessionShellAsEndedTab(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime() // no host for the handle: conclusively not alive
	rec := sessionShell("ptyhost-v1:shellterm-a")
	rec.LastAgentHarness = domain.HarnessClaudeCode
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	tracker := &recordingTracker{}
	svc.SetTerminalTrackers(ctx, tracker)

	reaped, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx)
	if err != nil || reaped != 0 {
		t.Fatalf("reap = %d, %v; want nothing deleted", reaped, err)
	}
	if len(st.records) != 1 || st.records[0].EndedAt == nil {
		t.Fatalf("rows = %+v; want the row kept and marked ended", st.records)
	}
	if !reflect.DeepEqual(rt.destroyed, []string{rec.HandleID}) {
		t.Fatalf("destroyed = %v; want the retained host torn down once", rt.destroyed)
	}
	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %+v, %v; want the ended tab visible", listed, err)
	}
	got := listed[0]
	if got.EndedAt == nil || got.Title != "Build" || got.SessionID != "session-1" || got.ResumeHarness != domain.HarnessClaudeCode {
		t.Fatalf("listed = %+v", got)
	}
	// Ended rows are not probed or torn down again.
	if len(rt.destroyed) != 1 {
		t.Fatalf("list re-destroyed an ended shell: %v", rt.destroyed)
	}
	if !reflect.DeepEqual(tracker.untracked, []string{rec.HandleID}) {
		t.Fatalf("untracked = %v; an ended shell's agent watch must stop", tracker.untracked)
	}
	// A restarted daemon does not follow an ended shell's agent.
	fresh := &recordingTracker{}
	svc.SetTerminalTrackers(ctx, fresh)
	if len(fresh.tracked) != 0 {
		t.Fatalf("tracked = %v; ended shells have no agent to follow", fresh.tracked)
	}
}

// A shell whose user typed `exit` during this launch is treated the same way:
// listed as ended until the user closes or reopens it.
func TestListMarksExitedSessionShellEnded(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	rec := sessionShell("shellterm-a")
	rec.AppRunID = testAppRunID
	rt.aliveByHandle[rec.HandleID] = true
	rt.childExited = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})

	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || listed[0].EndedAt == nil {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	if rt.aliveByHandle[rec.HandleID] || len(st.records) != 1 {
		t.Fatalf("host alive = %v, rows = %+v", rt.aliveByHandle[rec.HandleID], st.records)
	}
}

// Only a session's user shell is kept: trusted auth terminals and shells no
// client shows are still forgotten when their process is gone.
func TestRuntimeLossStillForgetsAuthAndSessionlessShells(t *testing.T) {
	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{
		{HandleID: "auth", AppRunID: "previous", Transient: true},
		{HandleID: "standalone", AppRunID: "previous", ProjectID: "project-1"},
		{HandleID: "current-standalone", AppRunID: testAppRunID, ProjectID: "project-1"},
	}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	if n, err := svc.ReapShellTerminalsFromPreviousAppRuns(context.Background()); err != nil || n != 2 {
		t.Fatalf("reap = %d, %v", n, err)
	}
	listed, err := svc.ListShellTerminalsForCurrentAppRun(context.Background())
	if err != nil || len(listed) != 0 || len(st.records) != 0 {
		t.Fatalf("list = %+v, %v; rows = %+v", listed, err, st.records)
	}
}

// An inconclusive probe still proves nothing: the shell is neither ended nor
// torn down, before or after a desktop relaunch.
func TestInconclusiveProbeDoesNotEndShell(t *testing.T) {
	for _, probeErr := range []error{ports.ErrRuntimeProbeInconclusive, ports.ErrRuntimeUnavailable, errors.New("status failed")} {
		rt := newFakeShellRuntime()
		rt.childProbeErr = probeErr
		rec := sessionShell("shellterm-a")
		st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}}
		svc := newTestService(rt, st, &fakeProjectRootLocator{})
		if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(context.Background()); err != nil {
			t.Fatal(err)
		}
		listed, err := svc.ListShellTerminalsForCurrentAppRun(context.Background())
		if err != nil || len(listed) != 1 || listed[0].EndedAt != nil || st.records[0].EndedAt != nil || len(rt.destroyed) != 0 {
			t.Fatalf("%v: list = %+v, %v; rows = %+v; destroyed = %v", probeErr, listed, err, st.records, rt.destroyed)
		}
	}
}

// Closing an ended tab is the user's explicit decision: its row goes, and
// there is no runtime left to destroy.
func TestCloseEndedShellRemovesRow(t *testing.T) {
	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{endedSessionShell("shellterm-a", "")}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	if err := svc.CloseShellTerminal(context.Background(), "shellterm-a"); err != nil {
		t.Fatal(err)
	}
	if len(st.records) != 0 || len(rt.destroyed) != 0 {
		t.Fatalf("rows = %+v, destroyed = %v", st.records, rt.destroyed)
	}
}

// Tearing down the parent session removes its ended tabs with its live ones.
func TestSessionTeardownRemovesEndedShells(t *testing.T) {
	rt := newFakeShellRuntime()
	live := sessionShell("shellterm-live")
	rt.aliveByHandle[live.HandleID] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{endedSessionShell("shellterm-ended", ""), live}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	release, err := svc.BeginSessionTeardown(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(st.records) != 0 || !reflect.DeepEqual(rt.destroyed, []string{"shellterm-live"}) {
		t.Fatalf("rows = %+v, destroyed = %v", st.records, rt.destroyed)
	}
}

func TestReopenEndedShellStartsFreshShellInSameTab(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	workspace := t.TempDir()
	ended := endedSessionShell("shellterm-old", domain.HarnessClaudeCode)
	ended.WorkingDir = workspace
	other := sessionShell("shellterm-other")
	other.CreatedAt = ended.CreatedAt.Add(time.Hour)
	rt.aliveByHandle[other.HandleID] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{ended, other}}
	svc := reopenTestService(t, rt, st, workspace)

	got, err := svc.ReopenShellTerminal(ctx, ReopenShellTerminalInput{HandleID: "shellterm-old", Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("ReopenShellTerminal: %v", err)
	}
	if got.HandleID == "shellterm-old" || got.EndedAt != nil || got.Title != "Build" || got.SessionID != "session-1" ||
		got.WorkingDir != workspace || !got.CreatedAt.Equal(ended.CreatedAt) {
		t.Fatalf("reopened = %+v", got)
	}
	if len(st.records) != 2 || st.records[0].HandleID != got.HandleID || !st.records[0].TitleExplicit ||
		st.records[0].LastAgentHarness != domain.HarnessClaudeCode || st.records[0].AppRunID != testAppRunID {
		t.Fatalf("rows = %+v; want the ended row replaced in place", st.records)
	}
	if len(rt.created) != 1 || rt.created[0].WorkspacePath != workspace || rt.created[0].Env["AO_SESSION_ID"] != "session-1" ||
		rt.created[0].Env["AO_CUE_READY_FILE"] != "" {
		t.Fatalf("created = %+v", rt.created)
	}
	// Nothing is typed into a plain reopened shell.
	select {
	case sent := <-rt.sentCh:
		t.Fatalf("reopen sent input %q", sent.input)
	default:
	}
}

func TestReopenFallsBackWhenDirectoryIsGone(t *testing.T) {
	rt := newFakeShellRuntime()
	workspace := t.TempDir()
	ended := endedSessionShell("shellterm-old", "")
	ended.WorkingDir = filepath.Join(t.TempDir(), "removed-worktree")
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{ended}}
	svc := reopenTestService(t, rt, st, workspace)
	got, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old"})
	if err != nil || got.WorkingDir != workspace {
		t.Fatalf("reopen = %+v, %v; want the session's current workspace", got, err)
	}
}

func TestReopenRefusesLiveUnknownOrUnresumable(t *testing.T) {
	rt := newFakeShellRuntime()
	live := sessionShell("shellterm-live")
	rt.aliveByHandle[live.HandleID] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{live, endedSessionShell("shellterm-plain", domain.HarnessAider)}}
	svc := reopenTestService(t, rt, st, t.TempDir())
	for _, tc := range []struct {
		in   ReopenShellTerminalInput
		code string
	}{
		{ReopenShellTerminalInput{HandleID: "shellterm-live"}, "SHELL_TERMINAL_NOT_ENDED"},
		{ReopenShellTerminalInput{HandleID: "shellterm-ghost"}, "SHELL_TERMINAL_NOT_FOUND"},
		{ReopenShellTerminalInput{HandleID: "shellterm-plain", ResumeAgent: true}, "SHELL_TERMINAL_AGENT_NOT_RESUMABLE"},
	} {
		_, err := svc.ReopenShellTerminal(context.Background(), tc.in)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
			t.Fatalf("%+v: err = %v, want %s", tc.in, err, tc.code)
		}
	}
	if len(rt.created) != 0 || len(st.records) != 2 {
		t.Fatalf("created = %d, rows = %+v; refused reopen changed state", len(rt.created), st.records)
	}
}

// Resuming types only the harness's own conversation picker command, after the
// new shell's first prompt. It is never anything the old shell ran.
func TestReopenResumeTypesOnlyAgentPicker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell delivery")
	}
	t.Setenv("SHELL", "/bin/bash")
	rt := newFakeShellRuntime()
	rt.cueReady = true
	workspace := t.TempDir()
	ended := endedSessionShell("shellterm-old", domain.HarnessCodex)
	ended.WorkingDir = workspace
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{ended}}
	svc := reopenTestService(t, rt, st, workspace)

	got, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old", ResumeAgent: true})
	if err != nil {
		t.Fatalf("ReopenShellTerminal: %v", err)
	}
	if rt.created[0].StartOnAttach || rt.created[0].Env["AO_CUE_READY_FILE"] == "" {
		t.Fatalf("created = %+v; a resume needs a started shell with a readiness signal", rt.created[0])
	}
	select {
	case sent := <-rt.sentCh:
		if sent.handleID != got.HandleID || !strings.Contains(sent.payload, "codex resume") {
			t.Fatalf("sent = %+v", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume command was not delivered")
	}
}

type recordingTracker struct{ tracked, untracked []string }

func (r *recordingTracker) TrackShellTerminal(handleID string) {
	r.tracked = append(r.tracked, handleID)
}
func (r *recordingTracker) UntrackShellTerminal(handleID string) {
	r.untracked = append(r.untracked, handleID)
}

// A tab is shown as ended only once that is stored: reopen reads the stored
// row and would refuse a tab the list showed as ended but never marked.
func TestEndedStateNotReportedUntilStored(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime() // the host is gone
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{sessionShell("shellterm-a")}, markEndedErr: errors.New("disk busy")}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})

	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || listed[0].EndedAt != nil || st.records[0].EndedAt != nil {
		t.Fatalf("list = %+v, %v; rows = %+v; want the row kept and not reported ended", listed, err, st.records)
	}
	st.markEndedErr = nil
	listed, err = svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || listed[0].EndedAt == nil || st.records[0].EndedAt == nil {
		t.Fatalf("retry list = %+v, %v; rows = %+v; want it ended once stored", listed, err, st.records)
	}
}

// A shell with no prompt signal cannot take the resume command, so the
// request is refused before the ended tab is replaced.
func TestReopenResumeRefusedForShellWithoutReadiness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell paths")
	}
	shell := filepath.Join(t.TempDir(), "nu")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := newFakeShellRuntime()
	workspace := t.TempDir()
	ended := endedSessionShell("shellterm-old", domain.HarnessClaudeCode)
	ended.WorkingDir = workspace
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{ended}}
	svc := reopenTestService(t, rt, st, workspace)

	_, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old", Shell: shell, ResumeAgent: true})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SHELL_TERMINAL_AGENT_RESUME_UNSUPPORTED" {
		t.Fatalf("err = %v, want SHELL_TERMINAL_AGENT_RESUME_UNSUPPORTED", err)
	}
	if len(rt.created) != 0 || len(st.records) != 1 || st.records[0].HandleID != "shellterm-old" || st.records[0].EndedAt == nil {
		t.Fatalf("created = %d, rows = %+v; want the ended tab unchanged", len(rt.created), st.records)
	}
	// A plain reopen still works in that shell.
	if _, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old", Shell: shell}); err != nil {
		t.Fatalf("plain reopen: %v", err)
	}
}
