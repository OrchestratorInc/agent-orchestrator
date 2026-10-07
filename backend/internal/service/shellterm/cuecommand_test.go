package shellterm

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestCueCommandInput(t *testing.T) {
	command := "printf '%s\\n' 'left\tright'\n# '\"雪\r\n"
	for _, shell := range []string{"bash", "zsh", "sh", "fish", "bash.exe", "sh.exe", "pwsh.exe", "powershell.exe"} {
		t.Run(shell, func(t *testing.T) {
			input, env := cueCommandInput([]string{filepath.Join("shells", shell)}, command)
			if env["AO_CUE_COMMAND"] != command {
				t.Fatalf("payload = %q, want %q", env["AO_CUE_COMMAND"], command)
			}
			if strings.ContainsAny(input, "\t\r\n") || strings.Contains(input, "left") || !strings.Contains(input, "AO_CUE_COMMAND") {
				t.Fatalf("input is not a fixed single-line dispatcher: %q", input)
			}
		})
	}
	for _, argv := range [][]string{nil, {"cmd.exe"}, {"unknown"}} {
		input, env := cueCommandInput(argv, command)
		if input != command || len(env) != 0 {
			t.Fatalf("legacy shell %v: input = %q, env = %v", argv, input, env)
		}
	}
}

func cueTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	t.Setenv("SHELL", "/bin/sh")
	return ""
}

func assertCueDelivery(t *testing.T, rt *fakeShellRuntime, handleID, command string) {
	t.Helper()
	sent := <-rt.sentCh
	cfg := rt.created[len(rt.created)-1]
	input, env := cueCommandInput(cfg.Argv, command)
	if sent.handleID != handleID || sent.input != input {
		t.Fatalf("sent = %+v, want handle %q and input %q", sent, handleID, input)
	}
	for key, value := range env {
		if cfg.Env[key] != value {
			t.Fatalf("launch environment %s = %q, want %q", key, cfg.Env[key], value)
		}
	}
}

func TestCueCommandOpensNewNormalProjectShellForEveryInvocation(t *testing.T) {
	root := t.TempDir()
	rt := newFakeShellRuntime()
	rt.cueReady = true
	st := &fakeShellTerminalStore{}
	svc := newTestService(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	input := RunCueCommandInput{ProjectID: "portfolio", Shell: cueTestShell(t), Command: `echo "héllo"`}
	first, err := svc.RunCueCommand(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkingDir != root || first.SessionID != "" || len(rt.created) != 1 || st.records[0].Transient {
		t.Fatalf("first terminal = %+v, creates = %+v, records = %+v", first, rt.created, st.records)
	}
	assertCueDelivery(t, rt, first.HandleID, input.Command)
	input.Command = "npm run build"
	second, err := svc.RunCueCommand(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.HandleID == first.HandleID || second.Title == first.Title || len(rt.created) != 2 || len(st.records) != 2 || st.records[1].Transient {
		t.Fatalf("second = %+v, creates = %+v", second, rt.created)
	}
	assertCueDelivery(t, rt, second.HandleID, input.Command)
}

func TestCueCommandReceivesProjectEnvironment(t *testing.T) {
	root := t.TempDir()
	rt := newFakeShellRuntime()
	rt.cueReady = true
	svc := newTestService(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{
		roots: map[domain.ProjectID]string{"portfolio": root},
		envs:  map[domain.ProjectID]map[string]string{"portfolio": {"PROJECT_TOKEN": "cue-value", "AO_CUE_READY_FILE": "untrusted", "AO_CUE_COMMAND": "untrusted"}},
	})
	svc.dataDir = t.TempDir()
	_, err := svc.RunCueCommand(context.Background(), RunCueCommandInput{ProjectID: "portfolio", Shell: cueTestShell(t), Command: "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.created[0].Env["PROJECT_TOKEN"]; got != "cue-value" {
		t.Fatalf("cue environment token = %q", got)
	}
	if got := rt.created[0].Env["AO_CUE_READY_FILE"]; got == "untrusted" || got == "" {
		t.Fatalf("cue readiness marker = %q", got)
	}
	if got := rt.created[0].Env["AO_CUE_COMMAND"]; got != "pwd" {
		t.Fatalf("cue command = %q", got)
	}
}

func TestCueCommandDoesNotSendToExistingTerminals(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	rt := newFakeShellRuntime()
	rt.cueReady = true
	st := &fakeShellTerminalStore{}
	sessions := &fakeSessionWorkspaceLocator{sessions: map[domain.SessionID]fakeSessionWorkspace{"session": {workspacePath: workspace, projectID: "portfolio", activity: domain.ActivityIdle}}}
	svc := newTestServiceWithSessions(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}}, sessions)
	svc.dataDir = t.TempDir()
	add := func(id string, project domain.ProjectID, session domain.SessionID, dir string, when time.Time) {
		st.records = append(st.records, ShellTerminalRecord{HandleID: id, ProjectID: project, SessionID: session, WorkingDir: dir, Title: id, CreatedAt: when})
		rt.aliveByHandle[id] = true
	}
	now := time.Now()
	add("old", "portfolio", "session", workspace, now.Add(-time.Hour))
	add("new", "portfolio", "session", workspace, now)
	add("board", "portfolio", "", root, now.Add(time.Hour))
	add("other", "other", "session", workspace, now.Add(2*time.Hour))
	rt.childProbeErr = errors.New("existing terminal probe failed")
	input := RunCueCommandInput{ProjectID: "portfolio", SessionID: "session", Shell: cueTestShell(t), Command: "pwd"}
	term, err := svc.RunCueCommand(context.Background(), input)
	if err != nil || term.HandleID == "old" || term.HandleID == "new" || term.WorkingDir != workspace || term.SessionID != "session" {
		t.Fatalf("new session terminal = %+v, err = %v", term, err)
	}
	assertCueDelivery(t, rt, term.HandleID, "pwd")
	firstHandle := term.HandleID
	term, err = svc.RunCueCommand(context.Background(), input)
	if err != nil || term.HandleID == firstHandle {
		t.Fatalf("next session terminal = %+v, err = %v", term, err)
	}
	assertCueDelivery(t, rt, term.HandleID, "pwd")
	input.SessionID = ""
	term, err = svc.RunCueCommand(context.Background(), input)
	if err != nil || term.HandleID == "board" || term.SessionID != "" || term.WorkingDir != root {
		t.Fatalf("new board terminal = %+v, err = %v", term, err)
	}
	assertCueDelivery(t, rt, term.HandleID, "pwd")
	if len(rt.created) != 3 {
		t.Fatalf("creates = %+v, want one per invocation", rt.created)
	}
}

func TestCueCommandUsesExactSessionWorktreeAndRejectsUnusableTargets(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name   string
		target fakeSessionWorkspace
		valid  bool
	}{
		{"active", fakeSessionWorkspace{workspacePath: workspace, projectID: "portfolio", activity: domain.ActivityIdle}, true},
		{"wrong project", fakeSessionWorkspace{workspacePath: workspace, projectID: "other"}, false},
		{"terminated", fakeSessionWorkspace{workspacePath: workspace, projectID: "portfolio", terminated: true}, false},
		{"exited", fakeSessionWorkspace{workspacePath: workspace, projectID: "portfolio", activity: domain.ActivityExited}, false},
		{"blocked", fakeSessionWorkspace{workspacePath: workspace, projectID: "portfolio", activity: domain.ActivityBlocked}, false},
		{"missing worktree", fakeSessionWorkspace{workspacePath: filepath.Join(workspace, "gone"), projectID: "portfolio"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newFakeShellRuntime()
			rt.cueReady = true
			svc := newTestServiceWithSessions(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}}, &fakeSessionWorkspaceLocator{sessions: map[domain.SessionID]fakeSessionWorkspace{"session": tc.target}})
			svc.dataDir = t.TempDir()
			term, err := svc.RunCueCommand(context.Background(), RunCueCommandInput{ProjectID: "portfolio", SessionID: "session", Shell: cueTestShell(t), Command: "pwd"})
			if tc.valid {
				if err != nil || term.WorkingDir != workspace || len(rt.created) != 1 {
					t.Fatalf("terminal = %+v, err = %v, creates = %+v", term, err, rt.created)
				}
			} else if err == nil || len(rt.created) != 0 {
				t.Fatalf("err = %v, creates = %+v", err, rt.created)
			}
		})
	}
}

func TestCueCommandDoesNotRetryAmbiguousSendFailure(t *testing.T) {
	root := t.TempDir()
	rt := newFakeShellRuntime()
	rt.cueReady = true
	rt.sendErr = errors.New("send failed")
	st := &fakeShellTerminalStore{}
	svc := newTestService(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	_, err := svc.RunCueCommand(context.Background(), RunCueCommandInput{ProjectID: "portfolio", Shell: cueTestShell(t), Command: "npm test"})
	if !errors.Is(err, rt.sendErr) || len(rt.created) != 1 || len(rt.sentCh) != 1 || len(st.records) != 1 || len(rt.destroyed) != 0 {
		t.Fatalf("err = %v, creates = %+v, sends = %d, records = %+v, destroyed = %+v", err, rt.created, len(rt.sentCh), st.records, rt.destroyed)
	}
}

func TestCueCommandWaitsForShellPrompt(t *testing.T) {
	root := t.TempDir()
	gate := make(chan struct{})
	rt := newFakeShellRuntime()
	rt.cueReadyGate = gate
	st := &fakeShellTerminalStore{}
	svc := newTestService(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	shell := cueTestShell(t)
	done := make(chan error, 1)
	go func() {
		_, err := svc.RunCueCommand(context.Background(), RunCueCommandInput{ProjectID: "portfolio", Shell: shell, Command: "pwd"})
		done <- err
	}()
	select {
	case sent := <-rt.sentCh:
		t.Fatalf("sent before shell readiness: %+v", sent)
	case <-time.After(2 * initialInputPollInterval):
	}
	close(gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cue did not dispatch after shell readiness")
	}
	assertCueDelivery(t, rt, st.records[0].HandleID, "pwd")
}

func TestCueCommandDoesNotSendWhenShellExitsBeforePrompt(t *testing.T) {
	root := t.TempDir()
	rt := newFakeShellRuntime()
	rt.childExited = true
	st := &fakeShellTerminalStore{}
	svc := newTestService(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	_, err := svc.RunCueCommand(context.Background(), RunCueCommandInput{ProjectID: "portfolio", Shell: cueTestShell(t), Command: "pwd"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "CUE_SHELL_NOT_READY" || len(rt.sentCh) != 0 || len(st.records) != 1 {
		t.Fatalf("err=%v sends=%d records=%d", err, len(rt.sentCh), len(st.records))
	}
}

func TestCueCommandDoesNotSendWhenShellReadinessTimesOut(t *testing.T) {
	previousTimeout := cueShellReadyTimeout
	cueShellReadyTimeout = 100 * time.Millisecond
	t.Cleanup(func() { cueShellReadyTimeout = previousTimeout })
	root := t.TempDir()
	rt := newFakeShellRuntime()
	st := &fakeShellTerminalStore{}
	svc := newTestService(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	_, err := svc.RunCueCommand(context.Background(), RunCueCommandInput{ProjectID: "portfolio", Shell: cueTestShell(t), Command: "pwd"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "CUE_SHELL_NOT_READY" || len(rt.sentCh) != 0 || len(st.records) != 1 {
		t.Fatalf("err=%v sends=%d records=%d", err, len(rt.sentCh), len(st.records))
	}
}

func TestCueCommandDoesNotSendWhenCanceledDuringReadiness(t *testing.T) {
	root := t.TempDir()
	rt := newFakeShellRuntime()
	rt.childProbeCh = make(chan struct{}, 1)
	// Let cancellation close both readiness contexts before the probe returns.
	rt.childProbeWaitForCancellation = true
	st := &fakeShellTerminalStore{}
	svc := newTestService(rt, st, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shell := cueTestShell(t)
	done := make(chan error, 1)
	go func() {
		_, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "portfolio", Shell: shell, Command: "pwd"})
		done <- err
	}()
	select {
	case <-rt.childProbeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("cue did not begin waiting for shell readiness")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || len(rt.sentCh) != 0 || len(st.records) != 1 {
			t.Fatalf("err=%v sends=%d records=%d", err, len(rt.sentCh), len(st.records))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled cue did not stop waiting")
	}
}

func TestCueCommandRejectsInvalidInputAndCancellation(t *testing.T) {
	svc := newTestService(newFakeShellRuntime(), &fakeShellTerminalStore{}, &fakeProjectRootLocator{})
	for _, input := range []RunCueCommandInput{{Command: "pwd"}, {ProjectID: "portfolio", Command: "  "}} {
		_, err := svc.RunCueCommand(context.Background(), input)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("err = %v", err)
		}
	}
	root := t.TempDir()
	rt := newFakeShellRuntime()
	svc = newTestService(rt, &fakeShellTerminalStore{}, &fakeProjectRootLocator{roots: map[domain.ProjectID]string{"portfolio": root}})
	svc.dataDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.RunCueCommand(ctx, RunCueCommandInput{ProjectID: "portfolio", Shell: cueTestShell(t), Command: "pwd"})
	if !errors.Is(err, context.Canceled) || len(rt.created) != 0 {
		t.Fatalf("err = %v, creates = %+v", err, rt.created)
	}
}
