package shellterm

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func agentControlFixture() (*Service, *fakeShellRuntime) {
	rt := newFakeShellRuntime()
	rt.aliveByHandle["sh-own"] = true
	rt.aliveByHandle["sh-other"] = true
	st := &fakeShellTerminalStore{records: []ShellTerminalRecord{
		{HandleID: "sh-own", SessionID: "s-1", AppRunID: testAppRunID},
		{HandleID: "sh-other", SessionID: "s-2", AppRunID: testAppRunID},
	}}
	return newTestService(rt, st, &fakeProjectRootLocator{}), rt
}

func apiErrCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestSessionTerminalControlReachesOnlyTheSessionsOwnShells(t *testing.T) {
	svc, rt := agentControlFixture()
	ctx := context.Background()

	if err := svc.TypeIntoSessionTerminal(ctx, "s-1", "sh-own", "ls", true); err != nil {
		t.Fatalf("type into own shell: %v", err)
	}
	if sent := <-rt.sentCh; sent.handleID != "sh-own" || sent.input != "ls" {
		t.Fatalf("sent = %+v", sent)
	}
	for _, err := range []error{
		svc.TypeIntoSessionTerminal(ctx, "s-1", "sh-other", "ls", true),
		svc.InterruptSessionTerminal(ctx, "s-1", "sh-other"),
		func() error { _, err := svc.SessionTerminalOutput(ctx, "s-1", "sh-other", 10); return err }(),
		svc.TypeIntoSessionTerminal(ctx, "s-1", "missing", "ls", true),
	} {
		if apiErrCode(err) != "PANE_TERMINAL_NOT_FOUND" {
			t.Fatalf("err = %v, want PANE_TERMINAL_NOT_FOUND", err)
		}
	}
}

func TestSessionTerminalControlInterruptsAndReads(t *testing.T) {
	svc, rt := agentControlFixture()
	ctx := context.Background()

	if err := svc.InterruptSessionTerminal(ctx, "s-1", "sh-own"); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if sent := <-rt.sentCh; sent.input != "\x03" {
		t.Fatalf("interrupt sent %q", sent.input)
	}
	// The rendered screen wins over raw output, and comes back as plain text.
	rt.setOutput("raw \x1b[31mbytes\x1b[m")
	rt.styledOutput = "$ ls   \n\x1b[34mREADME.md\x1b[m\n\n   \n"
	output, err := svc.SessionTerminalOutput(ctx, "s-1", "sh-own", 0)
	if err != nil || output != "$ ls\nREADME.md" {
		t.Fatalf("output = %q, %v", output, err)
	}
	// A rendered screen that is empty is the answer: the terminal shows nothing,
	// whatever raw output it once produced.
	rt.styledOutput = "   \n\n"
	output, err = svc.SessionTerminalOutput(ctx, "s-1", "sh-own", 0)
	if err != nil || output != "" {
		t.Fatalf("empty screen read %q, %v; want empty", output, err)
	}
	// The newline after the last row is not a row: one line asks for the last row.
	rt.styledOutput = "$ ls\nREADME.md\n"
	output, err = svc.SessionTerminalOutput(ctx, "s-1", "sh-own", 1)
	if err != nil || output != "README.md" {
		t.Fatalf("last line = %q, %v; want README.md", output, err)
	}
	// Without a rendered screen, raw output is cleaned of control sequences.
	rt.styledErr = errors.New("no surface")
	output, err = svc.SessionTerminalOutput(ctx, "s-1", "sh-own", 0)
	if err != nil || output != "raw bytes" {
		t.Fatalf("raw output = %q, %v", output, err)
	}
}

func TestSessionTerminalControlRefusesAnExitedShell(t *testing.T) {
	svc, rt := agentControlFixture()
	rt.childExited = true
	if err := svc.TypeIntoSessionTerminal(context.Background(), "s-1", "sh-own", "ls", true); apiErrCode(err) != "PANE_TERMINAL_EXITED" {
		t.Fatalf("err = %v, want PANE_TERMINAL_EXITED", err)
	}
}

func TestCloseSessionTerminalClosesOnlyTheSessionsOwnShell(t *testing.T) {
	svc, rt := agentControlFixture()
	rt.childExited = true // an exited shell can still be closed
	ctx := context.Background()
	if err := svc.CloseSessionTerminal(ctx, "s-1", "sh-other"); apiErrCode(err) != "PANE_TERMINAL_NOT_FOUND" {
		t.Fatalf("closing another session's shell: err = %v", err)
	}
	if err := svc.CloseSessionTerminal(ctx, "s-1", "sh-own"); err != nil {
		t.Fatalf("close own shell: %v", err)
	}
	if len(rt.destroyed) != 1 || rt.destroyed[0] != "sh-own" {
		t.Fatalf("destroyed = %v, want [sh-own]", rt.destroyed)
	}
}
