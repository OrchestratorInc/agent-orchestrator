package agentaccounts

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newDuplicateFixture(t *testing.T, device string) (*Service, *fakeClaude, *memStore, string) {
	t.Helper()
	root := t.TempDir()
	signedIn := map[string]string{}
	if device != "" {
		signedIn[""] = device
	}
	claude := &fakeClaude{signedInAs: signedIn}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	return New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil), claude, store, root
}

func addSaved(t *testing.T, svc *Service, claude *fakeClaude, root, email string) string {
	t.Helper()
	ctx := context.Background()
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	claude.signedInAs[filepath.Join(root, "claude", start.OperationID)] = email
	status, _, err := svc.VerifyLogin(ctx, start.OperationID)
	if err != nil || status != LoginComplete {
		t.Fatalf("add %s = %q, %v", email, status, err)
	}
	return start.OperationID
}

func viewByID(t *testing.T, views []AccountView, id string) AccountView {
	t.Helper()
	for _, view := range views {
		if view.Account.ID == id {
			return view
		}
	}
	t.Fatalf("no account %q in %+v", id, views)
	return AccountView{}
}

func TestListNamesTheAccountTheComputersLoginDuplicates(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		deviceAfter string
		before      string
		savedHome   func(home string, claude *fakeClaude)
		deviceFails bool
		makeDefault string
		want        bool
	}{
		{name: "terminal signs in after the account was added", before: "", deviceAfter: "a@example.com", want: true},
		{name: "terminal switches from another login", before: "b@example.com", deviceAfter: "a@example.com", want: true},
		{name: "email differs only in case", before: "", deviceAfter: "A@Example.COM", want: true},
		{name: "the computer's login is the default", before: "", deviceAfter: "a@example.com", makeDefault: "device", want: true},
		{name: "the saved account is the default", before: "", deviceAfter: "a@example.com", makeDefault: "saved", want: true},
		{name: "a failed check keeps the last good reading", before: "", deviceAfter: "a@example.com", deviceFails: true, want: true},
		{name: "the computer is on another login", before: "", deviceAfter: "b@example.com", want: false},
		{name: "the computer is signed out", before: "", deviceAfter: "", want: false},
		{
			name: "the saved account is signed out", before: "", deviceAfter: "a@example.com", want: false,
			savedHome: func(home string, claude *fakeClaude) { delete(claude.signedInAs, home) },
		},
		{
			name: "the saved folder now holds another login", before: "", deviceAfter: "a@example.com", want: false,
			savedHome: func(home string, claude *fakeClaude) { claude.signedInAs[home] = "c@example.com" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, claude, store, root := newDuplicateFixture(t, tc.before)
			saved := addSaved(t, svc, claude, root, "a@example.com")
			if tc.deviceAfter == "" {
				delete(claude.signedInAs, "")
			} else {
				claude.signedInAs[""] = tc.deviceAfter
			}
			if tc.savedHome != nil {
				tc.savedHome(filepath.Join(root, "claude", saved), claude)
			}
			switch tc.makeDefault {
			case "device":
				store.defaults[domain.AgentAccountProviderClaude] = domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude)
			case "saved":
				store.defaults[domain.AgentAccountProviderClaude] = saved
			}
			if _, err := svc.List(ctx, true); err != nil {
				t.Fatal(err)
			}
			if tc.deviceFails {
				claude.failing = map[string]bool{"": true}
			}
			views, err := svc.List(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			device := viewByID(t, views, domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude))
			got := device.SameLoginAs == saved
			if got != tc.want || (!tc.want && device.SameLoginAs != "") {
				t.Fatalf("device SameLoginAs = %q, want duplicate=%v of %q (device %+v)", device.SameLoginAs, tc.want, saved, device)
			}
			if view := viewByID(t, views, saved); view.SameLoginAs != "" {
				t.Fatalf("the saved account is never the folded one: %+v", view)
			}
			if tc.makeDefault == "device" && !device.Default {
				t.Fatal("folding must not hide that the computer's login is the default")
			}
		})
	}
}

func TestListDoesNotFoldAcrossProviders(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{}}
	codex := &fakeCodex{fakeClaude: fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude, codex}, root, fakeTerminals{}, nil)
	saved := addSaved(t, svc, claude, root, "a@example.com")

	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if view := viewByID(t, views, domain.DeviceAgentAccountID(domain.AgentAccountProviderCodex)); view.SameLoginAs != "" {
		t.Fatalf("the Codex login folded into Claude account %s: %+v", saved, view)
	}
}

func TestAddRefusesALoginTheComputerSignedInToSinceTheLastReading(t *testing.T) {
	ctx := context.Background()
	svc, claude, store, root := newDuplicateFixture(t, "")
	if _, err := svc.List(ctx, false); err != nil {
		t.Fatal(err)
	}
	claude.signedInAs[""] = "a@example.com"

	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[pending] = "a@example.com"
	status, view, err := svc.VerifyLogin(ctx, start.OperationID)
	if err != nil || status != LoginDuplicate || view == nil || !view.Account.Device() {
		t.Fatalf("add a login the computer holds = %q view=%+v err=%v", status, view, err)
	}
	if len(store.accounts) != 0 {
		t.Fatalf("duplicate saved: %+v", store.accounts)
	}
	if _, signedIn := claude.signedInAs[pending]; signedIn {
		t.Fatal("the refused sign-in kept its token")
	}
}

func TestAddRefusesTheLoginOfASignedOutSavedAccount(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		folder func(home string, claude *fakeClaude)
	}{
		{name: "signed out", folder: func(home string, claude *fakeClaude) { delete(claude.signedInAs, home) }},
		{name: "holds another login", folder: func(home string, claude *fakeClaude) { claude.signedInAs[home] = "c@example.com" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, claude, store, root := newDuplicateFixture(t, "")
			saved := addSaved(t, svc, claude, root, "a@example.com")
			tc.folder(filepath.Join(root, "claude", saved), claude)
			if _, err := svc.List(ctx, true); err != nil {
				t.Fatal(err)
			}

			start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
			if err != nil {
				t.Fatal(err)
			}
			claude.signedInAs[filepath.Join(root, "claude", start.OperationID)] = "A@example.com"
			status, view, err := svc.VerifyLogin(ctx, start.OperationID)
			if err != nil || status != LoginDuplicate || view == nil || view.Account.ID != saved {
				t.Fatalf("re-add a saved account's login = %q view=%+v err=%v", status, view, err)
			}
			if len(store.accounts) != 1 {
				t.Fatalf("accounts = %+v, want only the first", store.accounts)
			}
		})
	}
}

func startAdd(t *testing.T, svc *Service, claude *fakeClaude, root, email, org string) (LoginStatus, *AccountView) {
	t.Helper()
	start, err := svc.StartLogin(context.Background(), domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[home] = email
	claude.orgOf[home] = org
	status, view, err := svc.VerifyLogin(context.Background(), start.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	return status, view
}

func TestSameEmailInAnotherOrganizationIsAnotherLogin(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name                string
		deviceOrg, savedOrg string
		want                bool
	}{
		{name: "same organization", deviceOrg: "org-team", savedOrg: "org-team", want: true},
		{name: "another organization", deviceOrg: "org-personal", savedOrg: "org-team", want: false},
		{name: "the CLI reports no organization", deviceOrg: "", savedOrg: "org-team", want: true},
		{name: "the account reports no organization", deviceOrg: "org-personal", savedOrg: "", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, claude, _, root := newDuplicateFixture(t, "")
			claude.orgOf = map[string]string{}
			status, view := startAdd(t, svc, claude, root, "a@example.com", tc.savedOrg)
			if status != LoginComplete || view == nil {
				t.Fatalf("add = %q %+v", status, view)
			}
			claude.signedInAs[""] = "a@example.com"
			claude.orgOf[""] = tc.deviceOrg
			views, err := svc.List(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			device := viewByID(t, views, domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude))
			if got := device.SameLoginAs == view.Account.ID; got != tc.want || (!tc.want && device.SameLoginAs != "") {
				t.Fatalf("device SameLoginAs = %q, want duplicate=%v", device.SameLoginAs, tc.want)
			}
		})
	}
}

func TestAddAcceptsTheSameEmailInAnotherOrganization(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		holder func(t *testing.T, svc *Service, claude *fakeClaude, root string) string
	}{
		{name: "held by the CLI", holder: func(_ *testing.T, _ *Service, claude *fakeClaude, _ string) string {
			claude.signedInAs[""] = "a@example.com"
			claude.orgOf[""] = "org-team"
			return domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude)
		}},
		{name: "held by a signed-in saved account", holder: func(t *testing.T, svc *Service, claude *fakeClaude, root string) string {
			_, view := startAdd(t, svc, claude, root, "a@example.com", "org-team")
			return view.Account.ID
		}},
		{name: "held by a saved account signed out after a reading", holder: func(t *testing.T, svc *Service, claude *fakeClaude, root string) string {
			_, view := startAdd(t, svc, claude, root, "a@example.com", "org-team")
			delete(claude.signedInAs, view.Account.Home)
			delete(claude.orgOf, view.Account.Home)
			if _, err := svc.List(ctx, true); err != nil {
				t.Fatal(err)
			}
			return view.Account.ID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, claude, _, root := newDuplicateFixture(t, "")
			claude.orgOf = map[string]string{}
			holder := tc.holder(t, svc, claude, root)

			if status, view := startAdd(t, svc, claude, root, "A@example.com", "org-personal"); status != LoginComplete || view == nil || view.Account.ID == holder {
				t.Fatalf("add in another organization = %q %+v", status, view)
			}
			if status, view := startAdd(t, svc, claude, root, "a@example.com", "org-team"); status != LoginDuplicate || view == nil || view.Account.ID != holder {
				t.Fatalf("add in the same organization = %q %+v, want duplicate of %s", status, view, holder)
			}
		})
	}
}

func TestAddRefusesASignedOutAccountsEmailWhenItsOrganizationIsUnknown(t *testing.T) {
	svc, claude, store, root := newDuplicateFixture(t, "")
	claude.orgOf = map[string]string{}
	home := filepath.Join(root, "claude", "acct-a")
	store.accounts = []domain.AgentAccount{{ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: home, Email: "a@example.com"}}

	if status, view := startAdd(t, svc, claude, root, "a@example.com", "org-personal"); status != LoginDuplicate || view == nil || view.Account.ID != "acct-a" {
		t.Fatalf("add = %q %+v, want duplicate of acct-a", status, view)
	}
}

func TestSignInAgainAfterRestartIgnoresSignedOutAccountsOfUnknownOrganization(t *testing.T) {
	ctx := context.Background()
	svc, claude, store, root := newDuplicateFixture(t, "")
	claude.orgOf = map[string]string{}
	_, a := startAdd(t, svc, claude, root, "a@example.com", "org-1")
	_, b := startAdd(t, svc, claude, root, "a@example.com", "org-2")
	if a == nil || b == nil || len(store.accounts) != 2 {
		t.Fatalf("setup: a=%+v b=%+v accounts=%+v", a, b, store.accounts)
	}
	for _, home := range []string{a.Account.Home, b.Account.Home} {
		delete(claude.signedInAs, home)
		delete(claude.orgOf, home)
	}

	restarted := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	if _, err := restarted.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	signInAgain := func(account domain.AgentAccount, org string) (LoginStatus, *AccountView) {
		t.Helper()
		start, err := restarted.StartSignInAgain(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		claude.signedInAs[account.Home] = "a@example.com"
		claude.orgOf[account.Home] = org
		status, view, err := restarted.VerifyLogin(ctx, start.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		return status, view
	}

	if status, view := signInAgain(a.Account, "org-1"); status != LoginComplete || view == nil || view.Account.ID != a.Account.ID || !view.SignedIn {
		t.Fatalf("sign A in again = %q %+v", status, view)
	}
	if _, signedIn := claude.signedInAs[a.Account.Home]; !signedIn {
		t.Fatal("A's fresh sign-in was logged out")
	}
	if status, view := signInAgain(b.Account, "org-1"); status != LoginDuplicate || view == nil || view.Account.ID != a.Account.ID {
		t.Fatalf("sign B in again to A's organization = %q %+v, want duplicate of A", status, view)
	}
	if status, view := signInAgain(b.Account, "org-2"); status != LoginComplete || view == nil || view.Account.ID != b.Account.ID {
		t.Fatalf("sign B in again to its own organization = %q %+v", status, view)
	}
}

func TestListNamesEachAccountsOrganization(t *testing.T) {
	ctx := context.Background()
	svc, claude, _, root := newDuplicateFixture(t, "")
	claude.orgOf, claude.orgNameOf = map[string]string{}, map[string]string{}
	_, team := startAdd(t, svc, claude, root, "a@example.com", "org-team")
	claude.orgNameOf[team.Account.Home] = "Acme"
	_, personal := startAdd(t, svc, claude, root, "a@example.com", "org-personal")
	claude.orgNameOf[personal.Account.Home] = "Personal"
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if v := viewByID(t, views, team.Account.ID); v.OrgID != "org-team" || v.OrgName != "Acme" {
		t.Fatalf("team = %+v", v)
	}
	if v := viewByID(t, views, personal.Account.ID); v.OrgID != "org-personal" || v.OrgName != "Personal" {
		t.Fatalf("personal = %+v", v)
	}

	delete(claude.signedInAs, team.Account.Home)
	views, err = svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if v := viewByID(t, views, team.Account.ID); v.SignedIn || v.OrgID != "org-team" || v.OrgName != "Acme" {
		t.Fatalf("signed-out team keeps its organization: %+v", v)
	}
}
