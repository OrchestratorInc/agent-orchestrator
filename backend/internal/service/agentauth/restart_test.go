package agentauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestTerminalLoginExclusionSurvivesDaemonRestart(t *testing.T) {
	ctx := t.Context()
	dataDir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dataDir)
	rt := &retainedAuthRuntime{alive: make(map[string]bool)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	shells := shellterm.NewService(rt, store, nil, nil, dataDir, "same-app-launch", logger)
	first := New(foundExecutable("codex"), shells)
	terminal, err := first.Start(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := store.SelectShellTerminalByHandleID(ctx, terminal.Terminal.HandleID)
	if err != nil || !found || record.CommandOwner != "agentauth:codex" {
		t.Fatalf("persisted command owner = %q, found=%v, err=%v", record.CommandOwner, found, err)
	}
	// Ownership must not depend on the editable display title.
	if _, err := shells.RenameShellTerminal(ctx, terminal.Terminal.HandleID, "Renamed login"); err != nil {
		t.Fatal(err)
	}
	first.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen the actual database and rebuild both services, keeping only the
	// runtime and app-run identity as Electron does across a daemon restart.
	reopened, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	shells = shellterm.NewService(rt, reopened, nil, nil, dataDir, "same-app-launch", logger)
	second := loginTestService(t, "wait")
	second.terminals = shells
	launches := 0
	commandContext := second.commandContext
	second.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		launches++
		return commandContext(ctx, name, args...)
	}
	assertCode := func(err error, code string) {
		t.Helper()
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != code {
			t.Fatalf("error = %v, want %s", err, code)
		}
	}
	_, err = second.StartLogin(ctx, "codex", "device_code", "")
	assertCode(err, "AGENT_AUTH_IN_PROGRESS")
	_, err = second.Start(ctx, "codex")
	assertCode(err, "AGENT_AUTH_IN_PROGRESS")
	_, err = second.Logout(ctx, "codex")
	assertCode(err, "AGENT_AUTH_IN_PROGRESS")
	if rt.probes != 3 || launches != 0 {
		t.Fatalf("probes=%d background launches=%d, want 3 and 0", rt.probes, launches)
	}
	// More than one retained command can exist after the old overlap bug.
	// A dead first handle must not hide another live or unknown command.
	other, err := shells.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{
		Owner: "agentauth:codex", Argv: []string{"codex", "login"}, Title: "Another login",
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.alive[terminal.Terminal.HandleID] = false
	_, err = second.StartLogin(ctx, "codex", "device_code", "")
	assertCode(err, "AGENT_AUTH_IN_PROGRESS")
	rt.probeErr = errors.New("runtime unavailable")
	_, err = second.StartLogin(ctx, "codex", "device_code", "")
	assertCode(err, "AGENT_AUTH_TERMINAL_UNKNOWN")
	rt.probeErr = nil
	_, err = second.StartLogin(ctx, "codex", "device_code", "")
	assertCode(err, "AGENT_AUTH_IN_PROGRESS")
	if launches != 0 {
		t.Fatal("background command launched before confirmed exit")
	}
	rt.alive[other.HandleID] = false
	if _, err := shells.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{
		Owner: "agentauth:claude-code", Argv: []string{"claude", "auth", "login"}, Title: "Claude login",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.StartLogin(ctx, "codex", "device_code", ""); err != nil {
		t.Fatalf("confirmed child exit did not release exclusion: %v", err)
	}
	if launches != 1 {
		t.Fatalf("background launches after exit = %d, want 1", launches)
	}
}

func TestLoginRejectsFailedTerminalReconciliation(t *testing.T) {
	svc := loginTestService(t, "wait")
	svc.terminals = &recordingTerminalOpener{listErr: errors.New("database unavailable")}
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("command launched without reconciling retained terminals")
		return nil
	}
	_, err := svc.StartLogin(t.Context(), "codex", "device_code", "")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_TERMINAL_UNKNOWN" {
		t.Fatalf("error = %v, want AGENT_AUTH_TERMINAL_UNKNOWN", err)
	}
	// A failed listing releases the per-harness admission reservation for retry.
	svc.terminals = &recordingTerminalOpener{}
	if _, err := svc.Start(t.Context(), "codex"); err != nil {
		t.Fatalf("retry after listing failure: %v", err)
	}
}

type retainedAuthRuntime struct {
	alive    map[string]bool
	probeErr error
	probes   int
}

func (r *retainedAuthRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	handle := ports.RuntimeHandle{ID: "ptyhost-v1:" + string(cfg.SessionID)}
	r.alive[handle.ID] = true
	return handle, nil
}
func (r *retainedAuthRuntime) Destroy(_ context.Context, handle ports.RuntimeHandle) error {
	delete(r.alive, handle.ID)
	return nil
}
func (*retainedAuthRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return true, nil // Host/scrollback deliberately outlives the child.
}
func (r *retainedAuthRuntime) IsChildAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	r.probes++
	return r.alive[handle.ID], r.probeErr
}
func (*retainedAuthRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "", nil
}
func (*retainedAuthRuntime) SendInput(context.Context, ports.RuntimeHandle, string) error {
	return nil
}
func (*retainedAuthRuntime) SendMessage(context.Context, ports.RuntimeHandle, string) error {
	return nil
}
