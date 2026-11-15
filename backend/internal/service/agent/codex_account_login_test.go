package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

func TestAccountLoginUsesSessionCodexInsteadOfPATH(t *testing.T) {
	managedDir := t.TempDir()
	pathDir := t.TempDir()
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	managedCodex := filepath.Join(managedDir, name)
	pathCodex := filepath.Join(pathDir, name)
	for _, path := range []string{managedCodex, pathCodex} {
		if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", pathDir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".CMD")
	}
	binaryutil.SetManagedBinDir(managedDir)
	t.Cleanup(func() { binaryutil.SetManagedBinDir("") })
	if got, err := exec.LookPath("codex"); err != nil || got != pathCodex {
		t.Fatalf("PATH fixture = %q, %v; want %q", got, err, pathCodex)
	}

	for _, reauthenticate := range []bool{false, true} {
		name := "add account"
		if reauthenticate {
			name = "reauthenticate"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			svc := NewWithDeps(Deps{
				Context: t.Context(), CodexAccountRoot: filepath.Join(root, "accounts"),
				CodexPendingRoot: filepath.Join(root, "pending"), CodexSwitchStagingRoot: filepath.Join(root, "staging"),
				CodexGlobalHome: filepath.Join(root, "device"),
			})
			selected, err := svc.ResolveAgentBinary(t.Context(), "codex")
			if err != nil || selected != managedCodex {
				t.Fatalf("session Codex = %q, %v; want %q", selected, err, managedCodex)
			}
			manager := svc.codexAccounts
			manager.executable = func() (string, error) { return "/ao", nil }
			terminal := &fakeCodexLoginTerminal{result: shellterm.ShellTerminal{HandleID: "account-login"}}
			svc.SetCodexAccountLoginTerminalOpener(terminal)
			accountID := ""
			if reauthenticate {
				record := commitTestAccountWithCredential(t, manager.catalog, manager.pendingRoot,
					"b60a377d-da68-4a61-86f2-f31f04c571f2", testOAuthCredential("provider-account", "old-access"),
					ports.CodexAccountObservation{Authentication: domain.AgentAuthenticationAuthorized, Method: domain.CodexAuthMethodChatGPT})
				accountID = record.Snapshot.ID
			}
			started, err := manager.openLoginTerminal(t.Context(), accountID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := manager.cancelLogin(context.Background(), started.Operation.OperationID); err != nil {
					t.Error(err)
				}
			})
			opened := terminal.opened[0]
			if want := []string{"/ao", "codex-login", "--executable", managedCodex}; !slices.Equal(opened.Argv, want) {
				t.Fatalf("account-login command = %q; want %q (PATH contains %q)", opened.Argv, want, pathCodex)
			}
			if home := opened.Env["CODEX_HOME"]; home == "" || home != opened.WorkingDir || !pathWithin(manager.pendingRoot, home) {
				t.Fatalf("account login lost its private credential home: %#v", opened)
			}
		})
	}
}

func TestAccountLoginDoesNotFallBackWhenSessionCodexCannotBeResolved(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		err  error
	}{
		{name: "missing executable", err: ports.ErrAgentBinaryNotFound},
		{name: "empty executable"},
		{name: "blank executable", path: " \t"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			svc := NewWithDeps(Deps{
				Context: t.Context(), CodexAccountRoot: filepath.Join(root, "accounts"),
				CodexPendingRoot: filepath.Join(root, "pending"), CodexSwitchStagingRoot: filepath.Join(root, "staging"),
				CodexGlobalHome: filepath.Join(root, "device"),
			})
			adapter := &readinessTestAgent{resolve: func(context.Context) (string, error) { return tt.path, tt.err }}
			svc.agents = []agentregistry.HarnessAgent{{Harness: domain.HarnessCodex, Agent: adapter}}
			manager := svc.codexAccounts
			manager.executable = func() (string, error) { return "/ao", nil }
			terminal := &fakeCodexLoginTerminal{result: shellterm.ShellTerminal{HandleID: "account-login"}}
			svc.SetCodexAccountLoginTerminalOpener(terminal)
			_, err := manager.openLoginTerminal(t.Context(), "")
			var apiError *apierr.Error
			if !errors.As(err, &apiError) || apiError.Code != "CODEX_LOGIN_TERMINAL_UNAVAILABLE" {
				t.Fatalf("unresolved Codex login error = %v; want CODEX_LOGIN_TERMINAL_UNAVAILABLE", err)
			}
			if len(terminal.opened) != 0 || manager.cached().ActiveLogin != nil {
				t.Fatal("unresolved Codex started a terminal or retained a pending login")
			}
			if entries, err := os.ReadDir(manager.pendingRoot); !errors.Is(err, os.ErrNotExist) && (err != nil || len(entries) != 0) {
				t.Fatalf("unresolved Codex left pending files: %v, %v", entries, err)
			}

			adapter.resolve = func(context.Context) (string, error) { return "/resolved/codex", nil }
			started, err := manager.openLoginTerminal(t.Context(), "")
			if err != nil {
				t.Fatalf("retry after repairing executable resolution: %v", err)
			}
			if _, err := manager.cancelLogin(context.Background(), started.Operation.OperationID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
