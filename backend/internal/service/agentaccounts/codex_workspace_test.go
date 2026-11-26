package agentaccounts

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCodexWorkspacesWithSameEmailBindSeparately(t *testing.T) {
	ctx := t.Context()
	provider := domain.AgentAccountProviderCodex
	const email = "one@example.com"
	const personal = "11111111-1111-4111-8111-111111111111"
	const team = "22222222-2222-4222-8222-222222222222"
	codex := &fakeCodex{fakeClaude: fakeClaude{signedInAs: map[string]string{"": email}, orgOf: map[string]string{"": personal}, limitsPlan: "business"}}
	store := &memStore{bindings: map[domain.SessionID]string{}, defaults: map[domain.AgentAccountProvider]string{}}
	root := t.TempDir()
	svc := New(store, []ports.AgentAccountProviderCLI{codex}, root, fakeTerminals{}, nil)
	ui := &fakeUILogin{}
	svc.SetUILogin(ui)

	add := func(requested, observed string) (LoginStatus, *AccountView, error) {
		t.Helper()
		start, err := svc.StartLogin(ctx, provider, LoginRequest{Method: "browser", WorkspaceID: requested})
		if err != nil {
			t.Fatal(err)
		}
		home := filepath.Join(root, "codex", start.OperationID)
		codex.signedInAs[home], codex.orgOf[home] = email, observed
		return svc.VerifyLogin(ctx, start.OperationID)
	}
	if _, _, err := add(team, personal); !errors.Is(err, ports.ErrAgentAccountWorkspaceMismatch) || len(store.accounts) != 0 {
		t.Fatalf("wrong workspace was not rejected: %v", err)
	}
	if status, view, err := add(team, team); err != nil || status != LoginComplete || view == nil || view.OrgID != team {
		t.Fatalf("adding a second workspace: %s, %v, %v", status, view, err)
	}
	if status, view, err := add(team, team); err != nil || status != LoginDuplicate || view == nil || view.OrgID != team {
		t.Fatalf("adding the same workspace twice: %s, %v, %v", status, view, err)
	}
	if got := ui.started[0].workspaceID; got != team {
		t.Fatalf("sign-in was not constrained to the requested workspace: %q", got)
	}

	// A restart must not let a saved slot silently start work in another workspace.
	svc = New(store, []ports.AgentAccountProviderCLI{codex}, root, fakeTerminals{}, nil)
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := domain.DeviceAgentAccountID(provider)
	if viewByID(t, views, deviceID).SameLoginAs != "" {
		t.Fatal("different workspaces were combined into one choice")
	}
	if got := viewByID(t, views, store.accounts[0].ID).Account.Plan; got != "business" {
		t.Fatalf("plan from the limits read did not reach the account list: %q", got)
	}
	saved := store.accounts[0]
	for id, account := range map[domain.SessionID]string{"personal-task": deviceID, "team-task": saved.ID} {
		if err := svc.BindSession(ctx, id, domain.HarnessCodex, account); err != nil || store.bindings[id] != account {
			t.Fatalf("bind %s: %v (got %s)", account, err, store.bindings[id])
		}
	}
	if err := svc.SetDefault(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "default-task", domain.HarnessCodex, ""); err != nil || store.bindings["default-task"] != saved.ID {
		t.Fatalf("default workspace was not selected: %v", err)
	}
	codex.orgOf[saved.Home] = personal
	svc = New(store, []ports.AgentAccountProviderCLI{codex}, root, fakeTerminals{}, nil)
	if err := svc.BindSession(ctx, "wrong-workspace", domain.HarnessCodex, saved.ID); !errors.Is(err, ports.ErrAgentAccountSignedOut) {
		t.Fatalf("a saved account switched workspaces after restart: %v", err)
	}
}
