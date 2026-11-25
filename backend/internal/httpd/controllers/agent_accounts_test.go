package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

type apiKeyAgentAccounts struct {
	listOnlyAgentAccounts
	added, replaced []string
	err             error
}

func (s *apiKeyAgentAccounts) AddAPIKeyAccount(_ context.Context, p domain.AgentAccountProvider, name, key, color string) (agentaccounts.AccountView, error) {
	s.added = append(s.added, string(p)+"|"+name+"|"+key+"|"+color)
	return agentaccounts.AccountView{}, s.err
}

func (s *apiKeyAgentAccounts) ReplaceAPIKey(_ context.Context, id, key string) (agentaccounts.AccountView, error) {
	s.replaced = append(s.replaced, id+"|"+key)
	return agentaccounts.AccountView{}, s.err
}

// Adding or replacing a key answers with the account list, which names the
// account's kind and key hint but never the key; a refused or unchecked key is
// a client or availability error, not a server failure.
func TestAgentAccountsAPIKeyRoutes(t *testing.T) {
	const key = "sk-ant-api03-secret-0123456789abcdef"
	account := domain.AgentAccount{ID: "acct-key", Provider: domain.AgentAccountProviderClaude, Home: "/ao/claude/acct-key",
		Kind: domain.AgentAccountKindAPIKey, Name: "Work", KeyHint: "cdef"}
	svc := &apiKeyAgentAccounts{listOnlyAgentAccounts: listOnlyAgentAccounts{views: []agentaccounts.AccountView{{Account: account, LoggedIn: true}}}}
	r := chi.NewRouter()
	(&controllers.AgentAccountsController{Svc: svc}).Register(r)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	rec := send(http.MethodPost, "/agents/accounts/api-keys", `{"provider":"claude","name":"Work","apiKey":"`+key+`"}`)
	if rec.Code != http.StatusOK || len(svc.added) != 1 || svc.added[0] != "claude|Work|"+key+"|" {
		t.Fatalf("add: status %d, calls %v, body %s", rec.Code, svc.added, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("the response carries the key: %s", rec.Body)
	}
	var out struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 1 || out.Accounts[0]["kind"] != "api_key" || out.Accounts[0]["name"] != "Work" || out.Accounts[0]["keyHint"] != "cdef" {
		t.Fatalf("accounts = %v", out.Accounts)
	}

	if rec := send(http.MethodPut, "/agents/accounts/acct-key/api-key", `{"apiKey":"`+key+`"}`); rec.Code != http.StatusOK || len(svc.replaced) != 1 || svc.replaced[0] != "acct-key|"+key {
		t.Fatalf("replace: status %d, calls %v", rec.Code, svc.replaced)
	}
	if rec := send(http.MethodPost, "/agents/accounts/api-keys", `{"provider":"claude","apiKey":"  "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty key: status %d", rec.Code)
	}
	svc.err = ports.ErrAgentAccountKeyRejected
	if rec := send(http.MethodPost, "/agents/accounts/api-keys", `{"provider":"claude","apiKey":"bad"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "AGENT_ACCOUNT_KEY_REJECTED") {
		t.Fatalf("rejected key: status %d body %s", rec.Code, rec.Body)
	}
	svc.err = fmt.Errorf("%w: dial tcp: timeout", ports.ErrAgentAccountKeyUnchecked)
	if rec := send(http.MethodPut, "/agents/accounts/acct-key/api-key", `{"apiKey":"`+key+`"}`); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "AGENT_ACCOUNT_KEY_UNCHECKED") {
		t.Fatalf("unchecked key: status %d body %s", rec.Code, rec.Body)
	}
}

type identityAgentAccounts struct {
	controllers.AgentAccountService
	id          string
	name, color *string
	err         error
}

func (s *identityAgentAccounts) UpdateIdentity(_ context.Context, id string, name, color *string) (agentaccounts.AccountView, error) {
	s.id, s.name, s.color = id, name, color
	if s.err != nil {
		return agentaccounts.AccountView{}, s.err
	}
	account := domain.AgentAccount{ID: id, Provider: domain.AgentAccountProviderClaude, Home: "/ao/claude/" + id, Email: "a@example.com", Name: "Work"}
	if color != nil {
		account.Color = *color
	}
	return agentaccounts.AccountView{Account: account, SignedIn: true}, nil
}

// PATCH answers with the one updated account, color included; a bad color or
// body is a 400 and an unknown account a 404, all in the standard envelope.
func TestAgentAccountsUpdateIdentity(t *testing.T) {
	svc := &identityAgentAccounts{}
	r := chi.NewRouter()
	(&controllers.AgentAccountsController{Svc: svc}).Register(r)
	send := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body)))
		return rec
	}

	rec := send("/agents/accounts/acct-a", `{"name":"Work","color":"teal"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["id"] != "acct-a" || out["color"] != "teal" || out["name"] != "Work" {
		t.Fatalf("response = %v", out)
	}
	if svc.id != "acct-a" || svc.name == nil || *svc.name != "Work" || svc.color == nil || *svc.color != "teal" {
		t.Fatalf("service got %q %v %v", svc.id, svc.name, svc.color)
	}

	if rec := send("/agents/accounts/acct-a", `{"color":""}`); rec.Code != http.StatusOK || svc.name != nil || svc.color == nil || *svc.color != "" {
		t.Fatalf("clear color: status %d name %v color %v", rec.Code, svc.name, svc.color)
	}

	svc.err = ports.ErrAgentAccountColorInvalid
	rec = send("/agents/accounts/acct-a", `{"color":"mauve"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "AGENT_ACCOUNT_COLOR_INVALID") || !strings.Contains(rec.Body.String(), `"validation"`) {
		t.Fatalf("invalid color: status %d body %s", rec.Code, rec.Body)
	}
	svc.err = ports.ErrAgentAccountUnknown
	if rec := send("/agents/accounts/nope", `{"name":"x"}`); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "AGENT_ACCOUNT_NOT_FOUND") {
		t.Fatalf("unknown id: status %d body %s", rec.Code, rec.Body)
	}
	svc.err = nil
	for _, body := range []string{`{`, `{"name":5}`, `{"colour":"red"}`, ``} {
		if rec := send("/agents/accounts/acct-a", body); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_JSON") {
			t.Fatalf("body %q: status %d body %s", body, rec.Code, rec.Body)
		}
	}
}
