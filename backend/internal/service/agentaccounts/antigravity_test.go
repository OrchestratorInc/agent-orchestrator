package agentaccounts

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// fakeAgy is a provider whose CLI reads its folder from its command line and
// whose accounts have no limits, as Antigravity's.
type fakeAgy struct {
	*fakeClaude
	limitsRead int
}

func (*fakeAgy) Provider() domain.AgentAccountProvider {
	return domain.AgentAccountProviderAntigravity
}
func (*fakeAgy) HasLimits() bool { return false }
func (f *fakeAgy) Limits(context.Context, string) (domain.AgentAccountLimits, error) {
	f.limitsRead++
	return domain.AgentAccountLimits{}, ports.ErrAgentAccountNoLimits
}
func (*fakeAgy) HomeLoginArgv(_ context.Context, home string) ([]string, error) {
	return []string{"agy", "--gemini_dir=" + home}, nil
}
func (*fakeAgy) LaunchEnv(home string) map[string]string {
	if home == "" {
		return nil
	}
	return map[string]string{"GEMINI_HOME": home}
}

// argvTerminals records the command each sign-in terminal runs.
type argvTerminals struct{ argv []string }

func (t *argvTerminals) OpenCommandTerminal(_ context.Context, in shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	t.argv = in.Argv
	return shellterm.ShellTerminal{HandleID: "term-1"}, nil
}
func (*argvTerminals) CloseShellTerminal(context.Context, string) error { return nil }

// TestAntigravityAccountsSignInInTheirFolderAndHaveNoLimits pins how a
// provider without limits works: its sign-in names the account folder on the
// command line, its accounts are read for their login only, a session on one
// launches in its folder, and no session moves between them at a limit.
func TestAntigravityAccountsSignInInTheirFolderAndHaveNoLimits(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	agy := &fakeAgy{fakeClaude: &fakeClaude{signedInAs: map[string]string{"": ""}}}
	terminals := &argvTerminals{}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{agy}, root, terminals, nil)

	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderAntigravity, LoginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "antigravity", start.OperationID)
	if want := []string{"agy", "--gemini_dir=" + home}; !slices.Equal(terminals.argv, want) {
		t.Fatalf("sign-in argv = %v, want %v", terminals.argv, want)
	}
	agy.signedInAs[home] = "b@example.com"
	if status, view, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete || view == nil || !view.SignedIn {
		t.Fatalf("sign-in = %q view=%+v err=%v", status, view, err)
	}

	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Limits != nil || view.LimitsErr != "" || !view.SignedIn {
			t.Fatalf("%s: signed in=%v limits=%+v err=%q, want signed in with no limits", view.Account.ID, view.SignedIn, view.Limits, view.LimitsErr)
		}
	}
	if agy.limitsRead != 0 {
		t.Fatalf("limits read %d times for a provider without limits", agy.limitsRead)
	}

	if err := svc.BindSession(ctx, "s1", domain.HarnessAgy, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if env, _ := svc.SessionLaunchEnv(ctx, "s1"); env["GEMINI_HOME"] != home {
		t.Fatalf("session env = %v, want the account's folder", env)
	}
	if _, _, err := svc.SessionLimits(ctx, "s1", domain.HarnessAgy, true); !errors.Is(err, ports.ErrAgentAccountNoLimits) {
		t.Fatalf("session limits = %v, want ErrAgentAccountNoLimits", err)
	}
	if target, err := svc.HandoffTarget(ctx, "s1", domain.HarnessAgy, ""); err != nil || target != "" {
		t.Fatalf("handoff target = %q, %v, want none", target, err)
	}

	// The folder now holds another login: it is refused, as any account's.
	agy.signedInAs[home] = "c@example.com"
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s2", domain.HarnessAgy, start.OperationID); !errors.Is(err, ports.ErrAgentAccountSignedOut) {
		t.Fatalf("bind to a folder signed in as someone else: %v", err)
	}
}
