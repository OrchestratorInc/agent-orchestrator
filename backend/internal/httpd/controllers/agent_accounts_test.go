package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
)

type listOnlyAgentAccounts struct {
	controllers.AgentAccountService
	views []agentaccounts.AccountView
}

func (s listOnlyAgentAccounts) List(context.Context, bool) ([]agentaccounts.AccountView, error) {
	return s.views, nil
}

func TestAgentAccountsListNamesTheSameLogin(t *testing.T) {
	device := domain.AgentAccount{ID: domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude), Provider: domain.AgentAccountProviderClaude, Email: "a@example.com"}
	saved := domain.AgentAccount{ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: "/ao/claude/acct-a", Email: "a@example.com"}
	ctrl := &controllers.AgentAccountsController{Svc: listOnlyAgentAccounts{views: []agentaccounts.AccountView{
		{Account: device, SignedIn: true, SameLoginAs: saved.ID},
		{Account: saved, SignedIn: true, Default: true, OrgID: "org-1", OrgName: "Acme"},
	}}}
	r := chi.NewRouter()
	ctrl.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agents/accounts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 2 || out.Accounts[0]["sameLoginAs"] != saved.ID {
		t.Fatalf("device row = %v", out.Accounts)
	}
	if out.Accounts[1]["orgId"] != "org-1" || out.Accounts[1]["orgName"] != "Acme" {
		t.Fatalf("saved row organization = %v", out.Accounts[1])
	}
	if _, ok := out.Accounts[1]["sameLoginAs"]; ok {
		t.Fatalf("saved row names a same login: %v", out.Accounts[1])
	}
}

// The app words a failed check from its kind; the error text rides along as
// detail. The identity check's problem comes first, and a limits problem is
// shown only for a signed-in account.
func TestAgentAccountsListCarriesTheProblemKind(t *testing.T) {
	account := func(id string) domain.AgentAccount {
		return domain.AgentAccount{ID: id, Provider: domain.AgentAccountProviderClaude, Home: "/ao/claude/" + id}
	}
	ctrl := &controllers.AgentAccountsController{Svc: listOnlyAgentAccounts{views: []agentaccounts.AccountView{
		{Account: account("slow"), SignedIn: true, LimitsErr: "context deadline exceeded", LimitsProblem: domain.AgentAccountProblemSlow},
		{Account: account("other"), SignedIn: true, IdentityErr: "the folder is now signed in as c@example.com, not b@example.com", IdentityProblem: domain.AgentAccountProblemOtherLogin, LimitsErr: "x", LimitsProblem: domain.AgentAccountProblemUnavailable},
		{Account: account("out"), LimitsErr: "signed out"},
	}}}
	r := chi.NewRouter()
	ctrl.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agents/accounts", nil))
	var out struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Accounts) != 3 {
		t.Fatalf("status %d body %s: %v", rec.Code, rec.Body, err)
	}
	for i, want := range []struct{ problem, kind any }{
		{"context deadline exceeded", "slow"},
		{"the folder is now signed in as c@example.com, not b@example.com", "other_login"},
		{nil, nil},
	} {
		if got := out.Accounts[i]; got["problem"] != want.problem || got["problemKind"] != want.kind {
			t.Errorf("%s: problem %v kind %v, want %v %v", got["id"], got["problem"], got["problemKind"], want.problem, want.kind)
		}
	}
}
