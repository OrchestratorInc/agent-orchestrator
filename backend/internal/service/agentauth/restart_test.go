package agentauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
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

func TestManagedAccountLoginDoesNotExcludeMainAuthentication(t *testing.T) {
	for _, provider := range domain.AgentAccountProviders {
		for _, flow := range []string{"add", "sign-in-again"} {
			for _, action := range []string{"inline", "terminal", "logout"} {
				t.Run(string(provider)+"/"+flow+"/"+action, func(t *testing.T) {
					ctx := t.Context()
					dataDir := t.TempDir()
					store := sqlitetest.MustOpenAt(t, dataDir)
					rt := &retainedAuthRuntime{alive: make(map[string]bool)}
					logger := slog.New(slog.NewTextHandler(io.Discard, nil))
					shells := shellterm.NewService(rt, store, nil, nil, dataDir, "same-app-launch", logger)
					root := filepath.Join(dataDir, "accounts")
					accounts := agentaccounts.New(store, []ports.AgentAccountProviderCLI{isolatedAccountProvider{provider}}, root, shells, logger)
					var managed agentaccounts.LoginStart
					var err error
					if flow == "sign-in-again" {
						account := domain.AgentAccount{
							ID: "saved-account", Provider: provider, Home: filepath.Join(root, string(provider), "saved-account"),
							Email: "saved@example.com", CreatedAt: time.Now().UTC(),
						}
						if err := store.CreateAgentAccount(ctx, account); err != nil {
							t.Fatal(err)
						}
						managed, err = accounts.StartSignInAgain(ctx, account.ID)
					} else {
						managed, err = accounts.StartLogin(ctx, provider)
					}
					if err != nil {
						t.Fatal(err)
					}
					// Exercise the actual account caller, persisted shellterm ownership,
					// and main-auth admission while browser approval is still pending.
					auth := loginTestService(t, "wait")
					auth.terminals = shells
					switch action {
					case "inline":
						_, err = auth.StartLogin(ctx, "codex", "device_code", "")
					case "terminal":
						_, err = auth.Start(ctx, "codex")
					case "logout":
						_, err = auth.Logout(ctx, "codex")
					}
					if err != nil {
						t.Fatalf("managed %s %s blocked main Codex %s: %v", provider, flow, action, err)
					}
					record, found, err := store.SelectShellTerminalByHandleID(ctx, managed.Terminal.HandleID)
					if err != nil || !found || record.CommandOwner != "agentaccounts:"+string(provider) {
						t.Fatalf("managed command owner = %q, found=%v, err=%v", record.CommandOwner, found, err)
					}
					if !rt.alive[managed.Terminal.HandleID] {
						t.Fatal("managed account login must remain alive")
					}
				})
			}
		}
	}
}

// Only the provider CLI and runtime are fakes; the account, terminal, and
// authentication services above share the real SQLite store.
type isolatedAccountProvider struct{ provider domain.AgentAccountProvider }

func (p isolatedAccountProvider) Provider() domain.AgentAccountProvider { return p.provider }
func (isolatedAccountProvider) Identity(context.Context, string) (domain.AgentAccountIdentityCheck, error) {
	return domain.AgentAccountIdentityCheck{}, nil
}
func (isolatedAccountProvider) Limits(context.Context, string) (domain.AgentAccountLimits, error) {
	return domain.AgentAccountLimits{}, nil
}
func (p isolatedAccountProvider) LoginArgv(context.Context) ([]string, error) {
	if p.provider == domain.AgentAccountProviderClaude {
		return []string{"claude", "auth", "login"}, nil
	}
	return []string{"codex", "login"}, nil
}
func (isolatedAccountProvider) Logout(context.Context, string) error { return nil }
func (isolatedAccountProvider) Prepare(string) error                 { return nil }
func (p isolatedAccountProvider) LaunchEnv(home string) map[string]string {
	if p.provider == domain.AgentAccountProviderClaude {
		return map[string]string{"CLAUDE_CONFIG_DIR": home}
	}
	return map[string]string{"CODEX_HOME": home}
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
