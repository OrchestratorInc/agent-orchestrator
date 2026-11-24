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

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func (f *fakeShellTerminalStore) MarkShellTerminalEnded(ctx context.Context, handleID string, end ShellTerminalEnd) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.markEndedErr != nil {
		return false, f.markEndedErr
	}
	for i, rec := range f.records {
		if rec.HandleID == handleID && rec.EndedAt == nil {
			at := end.EndedAt
			f.records[i].EndedAt = &at
			f.records[i].WorkingDir = end.WorkingDir
			f.records[i].LastAgentHarness = end.AgentHarness
			f.records[i].AgentConversationID = end.AgentConversationID
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

func (f *fakeShellTerminalStore) AdoptShellTerminal(_ context.Context, handleID, appRunID string) (bool, error) {
	if f.adoptErr != nil {
		return false, f.adoptErr
	}
	for i, rec := range f.records {
		if rec.HandleID == handleID && rec.EndedAt == nil {
			f.records[i].AppRunID = appRunID
			return true, nil
		}
	}
	return false, nil
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

func TestRuntimeLossKeepsSessionShellAsEndedTab(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
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
	if len(rt.destroyed) != 1 {
		t.Fatalf("list re-destroyed an ended shell: %v", rt.destroyed)
	}
	if !reflect.DeepEqual(tracker.untracked, []string{rec.HandleID}) {
		t.Fatalf("untracked = %v; an ended shell's agent watch must stop", tracker.untracked)
	}
	fresh := &recordingTracker{}
	svc.SetTerminalTrackers(ctx, fresh)
	if len(fresh.tracked) != 0 {
		t.Fatalf("tracked = %v; ended shells have no agent to follow", fresh.tracked)
	}
}

func TestListClosesShellExitedDuringThisLaunch(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	rec := sessionShell("shellterm-a")
	rec.AppRunID = testAppRunID
	rt.aliveByHandle[rec.HandleID] = true
	rt.childExited = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})

	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	if rt.aliveByHandle[rec.HandleID] || len(st.records) != 0 {
		t.Fatalf("host alive = %v, rows = %+v", rt.aliveByHandle[rec.HandleID], st.records)
	}
}

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

func TestCloseRemovesSavedOutputSavedAfterItWasRead(t *testing.T) {
	rt := newFakeShellRuntime()
	live := sessionShell("shellterm-a")
	rt.aliveByHandle[live.HandleID] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{live}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	svc.dataDir = t.TempDir()
	saved := svc.savedOutputPath(live.HandleID)
	if err := os.MkdirAll(filepath.Dir(saved), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved, []byte("old output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseShellTerminal(context.Background(), live.HandleID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(saved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saved output kept after close: %v", err)
	}
}

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
		st.records[0].LastAgentHarness != "" || st.records[0].AppRunID != testAppRunID {
		t.Fatalf("rows = %+v; want the ended row replaced in place, with no agent running", st.records)
	}
	if len(rt.created) != 1 || rt.created[0].WorkspacePath != workspace || rt.created[0].Env["AO_SESSION_ID"] != "session-1" ||
		rt.created[0].Env["AO_CUE_READY_FILE"] != "" {
		t.Fatalf("created = %+v", rt.created)
	}
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

func TestEndingShellForgetsInputTimestamp(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{sessionShell("shellterm-a")}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	forgetter := &fakeTerminalInputForgetter{}
	svc.SetTerminalInputForgetter(forgetter)

	if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListShellTerminalsForCurrentAppRun(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forgetter.forgotten, []string{"shellterm-a"}) {
		t.Fatalf("forgotten = %v, want the ended handle once", forgetter.forgotten)
	}

	rt2 := newFakeShellRuntime()
	rt2.childProbeErr = ports.ErrRuntimeProbeInconclusive
	st2 := &fakeShellTerminalStore{records: []ShellTerminalRecord{sessionShell("shellterm-b")}}
	svc2 := newTestService(rt2, st2, &fakeProjectRootLocator{})
	forgetter2 := &fakeTerminalInputForgetter{}
	svc2.SetTerminalInputForgetter(forgetter2)
	if _, err := svc2.ListShellTerminalsForCurrentAppRun(ctx); err != nil {
		t.Fatal(err)
	}
	if len(forgetter2.forgotten) != 0 {
		t.Fatalf("forgotten = %v after an inconclusive probe", forgetter2.forgotten)
	}
}

func TestEndedStateNotReportedUntilStored(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{sessionShell("shellterm-a")}, markEndedErr: errors.New("disk busy")}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})

	if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil || len(st.records) != 1 || st.records[0].EndedAt != nil {
		t.Fatalf("reap err = %v; rows = %+v; want the row kept and not ended", err, st.records)
	}
	st.markEndedErr = nil
	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || listed[0].EndedAt == nil || st.records[0].EndedAt == nil {
		t.Fatalf("retry list = %+v, %v; rows = %+v; want it ended once stored", listed, err, st.records)
	}
}

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
	if _, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old", Shell: shell}); err != nil {
		t.Fatalf("plain reopen: %v", err)
	}
}

type fakeForegroundReader struct {
	dir, conversationID string
	harness             domain.AgentHarness
	ok                  bool
	read                []string
}

func (f *fakeForegroundReader) ReadShellForeground(_ context.Context, handleID string) (string, domain.AgentHarness, string, bool) {
	f.read = append(f.read, handleID)
	return f.dir, f.harness, f.conversationID, f.ok
}

func TestSuspendSessionShellsSavesShellsToRestore(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	rt.setOutput("$ make test\nok\n")
	running := sessionShell("shellterm-running")
	exited := sessionShell("shellterm-exited")
	ended := endedSessionShell("shellterm-ended", "")
	running.AppRunID, exited.AppRunID = testAppRunID, testAppRunID
	rt.aliveByHandle[running.HandleID] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{running, exited, ended}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	svc.dataDir = t.TempDir()
	const id = "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	fg := &fakeForegroundReader{dir: "/worktree/api", harness: domain.HarnessClaudeCode, conversationID: id, ok: true}
	svc.SetForegroundReader(fg)

	release, err := svc.SuspendSessionShells(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	release()

	if len(st.records) != 2 || st.records[0].HandleID != running.HandleID || st.records[1].HandleID != ended.HandleID {
		t.Fatalf("rows = %+v; want the running shell kept, the exited one closed, the ended one untouched", st.records)
	}
	got := st.records[0]
	if got.EndedAt == nil || got.WorkingDir != "/worktree/api" || got.LastAgentHarness != domain.HarnessClaudeCode || got.AgentConversationID != id {
		t.Fatalf("saved = %+v", got)
	}
	if !reflect.DeepEqual(fg.read, []string{running.HandleID}) {
		t.Fatalf("foreground read for %v", fg.read)
	}
	saved, err := os.ReadFile(svc.savedOutputPath(running.HandleID))
	if err != nil || !strings.HasPrefix(string(saved), "$ make test\nok\n") || !strings.HasSuffix(string(saved), savedOutputReset) {
		t.Fatalf("saved output = %q, %v", saved, err)
	}
	if rt.aliveByHandle[running.HandleID] {
		t.Fatal("the running shell's host was left running")
	}
}

func TestReopenRestoresSavedOutputAndExactConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell delivery")
	}
	t.Setenv("SHELL", "/bin/bash")
	rt := newFakeShellRuntime()
	rt.cueReady = true
	workspace := t.TempDir()
	const id = "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	ended := endedSessionShell("shellterm-old", domain.HarnessClaudeCode)
	ended.WorkingDir = workspace
	ended.AgentConversationID = id
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{ended}}
	svc := reopenTestService(t, rt, st, workspace)
	saved := svc.savedOutputPath(ended.HandleID)
	if err := os.MkdirAll(filepath.Dir(saved), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved, []byte("old output\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old", ResumeAgent: true})
	if err != nil {
		t.Fatalf("ReopenShellTerminal: %v", err)
	}
	if rt.created[0].ReplayFile != saved || rt.created[0].OutputLines != savedOutputLines {
		t.Fatalf("replay file = %q, output lines = %d; want %q, %d", rt.created[0].ReplayFile, rt.created[0].OutputLines, saved, savedOutputLines)
	}
	if env := rt.created[0].Env; env["AO_SESSION_ID"] != string(ended.SessionID) || env["AO_CUE_READY_FILE"] == "" {
		t.Fatalf("resumed shell env lost AO values: AO_SESSION_ID=%q AO_CUE_READY_FILE=%q", env["AO_SESSION_ID"], env["AO_CUE_READY_FILE"])
	}
	if _, err := os.Stat(saved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saved output kept after restore: %v", err)
	}
	if st.records[0].LastAgentHarness != domain.HarnessClaudeCode || st.records[0].AgentConversationID != "" {
		t.Fatalf("row = %+v", st.records[0])
	}
	select {
	case sent := <-rt.sentCh:
		if sent.handleID != got.HandleID || !strings.Contains(sent.payload, "claude --resume "+id) {
			t.Fatalf("sent = %+v", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume command was not delivered")
	}
}

func TestReopenInFallbackFolderOpensConversationPicker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell delivery")
	}
	t.Setenv("SHELL", "/bin/bash")
	rt := newFakeShellRuntime()
	rt.cueReady = true
	workspace := t.TempDir()
	ended := endedSessionShell("shellterm-old", domain.HarnessClaudeCode)
	ended.WorkingDir = filepath.Join(t.TempDir(), "removed")
	ended.AgentConversationID = "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{ended}}
	svc := reopenTestService(t, rt, st, workspace)
	if _, err := svc.ReopenShellTerminal(context.Background(), ReopenShellTerminalInput{HandleID: "shellterm-old", ResumeAgent: true}); err != nil {
		t.Fatalf("ReopenShellTerminal: %v", err)
	}
	select {
	case sent := <-rt.sentCh:
		if !strings.Contains(sent.payload, "claude --resume") || strings.Contains(sent.payload, ended.AgentConversationID) {
			t.Fatalf("sent = %+v, want the picker in the fallback folder", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume command was not delivered")
	}
}

func TestAgentResumeCommand(t *testing.T) {
	const id = "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	for _, tc := range []struct {
		harness domain.AgentHarness
		id      string
		want    string
	}{
		{domain.HarnessClaudeCode, id, "claude --resume " + id},
		{domain.HarnessClaudeCode, "", "claude --resume"},
		{domain.HarnessClaudeCode, "x; rm -rf ~", "claude --resume"},
		{domain.HarnessCodex, id, "codex resume -c check_for_update_on_startup=false " + id},
		{domain.HarnessCodex, "", "codex resume -c check_for_update_on_startup=false"},
		{domain.HarnessAider, id, ""},
	} {
		if got := agentResumeCommand(tc.harness, tc.id); got != tc.want {
			t.Errorf("agentResumeCommand(%s, %q) = %q, want %q", tc.harness, tc.id, got, tc.want)
		}
	}
}

func TestListDoesNotPruneShellEndedBySuspension(t *testing.T) {
	ctx := context.Background()
	live := sessionShell("shellterm-a")
	live.AppRunID = testAppRunID
	saved := live
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	saved.EndedAt = &at

	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{saved}, staleRestorable: []ShellTerminalRecord{live}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || listed[0].EndedAt == nil || len(st.records) != 1 || len(rt.destroyed) != 0 {
		t.Fatalf("list = %+v, %v; rows = %+v; destroyed = %v; want the saved row kept as ended", listed, err, st.records, rt.destroyed)
	}

	busy := &fakeShellTerminalStore{records: []ShellTerminalRecord{live}}
	svc = newTestService(newFakeShellRuntime(), busy, &fakeProjectRootLocator{})
	release, err := svc.acquireSessionGate(ctx, live.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err = svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || len(busy.records) != 1 {
		t.Fatalf("list under a busy gate = %+v, %v; rows = %+v; want the row left for later", listed, err, busy.records)
	}
	release()
	if listed, err = svc.ListShellTerminalsForCurrentAppRun(ctx); err != nil || len(listed) != 0 || len(busy.records) != 0 {
		t.Fatalf("list after release = %+v, %v; rows = %+v; want the exited shell pruned", listed, err, busy.records)
	}
}

func TestEndShellStoresEndAfterQuitDeadline(t *testing.T) {
	rec := sessionShell("shellterm-a")
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}}
	svc := newTestService(newFakeShellRuntime(), st, &fakeProjectRootLocator{})
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	end := unchangedEnd(rec)
	end.AgentHarness, end.AgentConversationID = domain.HarnessClaudeCode, "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	if stillAlive, err := svc.endShell(expired, rec, end); stillAlive || err != nil {
		t.Fatalf("endShell = %v, %v", stillAlive, err)
	}
	if st.records[0].EndedAt == nil || st.records[0].AgentConversationID != end.AgentConversationID {
		t.Fatalf("row = %+v; want the end stored despite the expired quit context", st.records[0])
	}
}

// The startup reap runs once per launch. A rebooted shell whose recovery it
// could not finish (here, an inconclusive probe) is ended to restore by the
// next list, not pruned as if it had exited during this launch.
func TestListFinishesUnfinishedStartupRecovery(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime() // the host is gone after the reboot
	rt.childProbeErr = ports.ErrRuntimeProbeInconclusive
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{sessionShell("shellterm-a")}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil || st.records[0].EndedAt != nil {
		t.Fatalf("reap err = %v; rows = %+v; want the row left for retry", err, st.records)
	}
	rt.childProbeErr = nil
	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 1 || listed[0].EndedAt == nil || len(st.records) != 1 || st.records[0].EndedAt == nil {
		t.Fatalf("list = %+v, %v; rows = %+v; want the tab kept and ended", listed, err, st.records)
	}
}

// A shell the startup reap found alive belongs to this launch from then on,
// so exiting it later closes the tab like any shell opened in this launch.
func TestShellRecoveredAliveClosesWhenItExits(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	rec := sessionShell("shellterm-a")
	rt.aliveByHandle[rec.HandleID] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil || st.records[0].AppRunID != testAppRunID {
		t.Fatalf("reap err = %v; rows = %+v; want the live shell adopted into this launch", err, st.records)
	}
	rt.childExited = true
	listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx)
	if err != nil || len(listed) != 0 || len(st.records) != 0 {
		t.Fatalf("list = %+v, %v; rows = %+v; want the exited shell closed", listed, err, st.records)
	}
}

func TestQuitKeepsAShellWhoseStartupRecoveryDidNotSettle(t *testing.T) {
	ctx := context.Background()
	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{sessionShell("shellterm-a")}, markEndedErr: errors.New("disk busy")}
	svc := newTestService(rt, st, &fakeProjectRootLocator{})
	svc.dataDir = t.TempDir()
	if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil {
		t.Fatal(err)
	}
	st.markEndedErr = nil
	release, err := svc.SuspendSessionShells(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(st.records) != 1 || st.records[0].EndedAt == nil {
		t.Fatalf("rows = %+v; want the tab kept as ended", st.records)
	}
}

// A shell whose startup recovery failed (an unknown probe, or a failed
// adoption) and that a later list finds alive is recovered then: once the
// user exits it, the tab closes rather than being ended and reopened.
func TestShellRecoveredAliveByLaterListClosesWhenItExits(t *testing.T) {
	for _, tc := range []struct {
		name               string
		probeErr, adoptErr error
	}{
		{name: "unknown startup probe", probeErr: ports.ErrRuntimeProbeInconclusive},
		{name: "failed startup adoption", adoptErr: errors.New("disk busy")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rt := newFakeShellRuntime()
			rec := sessionShell("shellterm-a")
			rt.aliveByHandle[rec.HandleID] = true
			rt.childProbeErr = tc.probeErr
			st := &fakeShellTerminalStore{records: []ShellTerminalRecord{rec}, adoptErr: tc.adoptErr}
			svc := newTestService(rt, st, &fakeProjectRootLocator{})
			if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil || st.records[0].AppRunID == testAppRunID {
				t.Fatalf("reap err = %v; rows = %+v; want the startup recovery unfinished", err, st.records)
			}
			rt.childProbeErr, st.adoptErr = nil, nil
			if listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx); err != nil || len(listed) != 1 || st.records[0].AppRunID != testAppRunID {
				t.Fatalf("live list = %+v, %v; rows = %+v; want the live shell adopted", listed, err, st.records)
			}
			rt.childExited = true
			if listed, err := svc.ListShellTerminalsForCurrentAppRun(ctx); err != nil || len(listed) != 0 || len(st.records) != 0 {
				t.Fatalf("list after exit = %+v, %v; rows = %+v; want the tab removed", listed, err, st.records)
			}
		})
	}
}
