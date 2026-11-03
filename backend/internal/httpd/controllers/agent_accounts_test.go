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

// The computer's login signed in as a saved account's login names that account,
// so the app can show the two as one.
func TestAgentAccountsListNamesTheSameLogin(t *testing.T) {
	device := domain.AgentAccount{ID: domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude), Provider: domain.AgentAccountProviderClaude, Email: "a@example.com"}
	saved := domain.AgentAccount{ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: "/ao/claude/acct-a", Email: "a@example.com"}
	ctrl := &controllers.AgentAccountsController{Svc: listOnlyAgentAccounts{views: []agentaccounts.AccountView{
		{Account: device, SignedIn: true, SameLoginAs: saved.ID},
		{Account: saved, SignedIn: true, Default: true},
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
	if _, ok := out.Accounts[1]["sameLoginAs"]; ok {
		t.Fatalf("saved row names a same login: %v", out.Accounts[1])
	}
}
