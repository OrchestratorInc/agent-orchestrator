package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountHTTPFake struct {
	state             domain.ProviderAccountState
	route             domain.ProviderSessionRoute
	managed, recovery bool
	err               error
	calls             []string
	replacement       string
	signOut           bool
	moveExisting      *bool
	renameID          string
	renameName        string
}

type usageHTTPFake struct {
	*accountHTTPFake
	usage map[string]domain.ProviderAccountUsage
	calls int
}

func (f *usageHTTPFake) AccountUsages(context.Context, []domain.ProviderAccount) map[string]domain.ProviderAccountUsage {
	f.calls++
	return f.usage
}

func (f *accountHTTPFake) State(context.Context) (domain.ProviderAccountState, error) {
	return f.state, f.err
}
func (f *accountHTTPFake) SetPrimary(_ context.Context, id string) error {
	f.calls = append(f.calls, "primary:"+id)
	return f.err
}
func (f *accountHTTPFake) SetPrimaryWithOptions(_ context.Context, id string, moveExisting bool) error {
	f.calls = append(f.calls, "primary:"+id)
	f.moveExisting = &moveExisting
	return f.err
}
func (f *accountHTTPFake) Remove(_ context.Context, id, replacement string, signOut bool) error {
	f.calls = append(f.calls, "remove:"+id)
	f.replacement = replacement
	f.signOut = signOut
	return f.err
}
func (f *accountHTTPFake) Switch(_ context.Context, id domain.SessionID, account string) error {
	f.calls = append(f.calls, "switch:"+string(id)+":"+account)
	return f.err
}
func (f *accountHTTPFake) SessionAccount(_ context.Context, id domain.SessionID) (domain.ProviderSessionRoute, bool, error) {
	f.calls = append(f.calls, "session:"+string(id))
	return f.route, f.managed, f.err
}
func (f *accountHTTPFake) RecoveryRequired(context.Context) (bool, error) { return f.recovery, f.err }
func (f *accountHTTPFake) Rename(_ context.Context, id, name string) error {
	f.renameID, f.renameName = id, name
	return f.err
}

type loginHTTPFake struct {
	login ports.ProviderLogin
	err   error
	calls []string
}

func (f *loginHTTPFake) Start(_ context.Context, p, id string) (ports.ProviderLogin, error) {
	f.calls = append(f.calls, "start:"+p+":"+id)
	return f.login, f.err
}
func (f *loginHTTPFake) Status(_ context.Context, id string) (ports.ProviderLogin, error) {
	f.calls = append(f.calls, "status:"+id)
	return f.login, f.err
}
func (f *loginHTTPFake) Cancel(_ context.Context, id string) error {
	f.calls = append(f.calls, "cancel:"+id)
	return f.err
}
func accountHTTPRequest(t *testing.T, c *controllers.ProviderAccountsController, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	c.Register(r)
	out := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(out, req)
	return out
}
func TestProviderAccountsHTTPSafeInventory(t *testing.T) {
	f := &accountHTTPFake{state: domain.ProviderAccountState{
		Accounts: []domain.ProviderAccount{
			{ID: "a", Provider: "codex", Email: "a@example.test", CredentialRef: "PRIVATE-FILE", AuthID: "PRIVATE-AUTH"},
			{ID: "b", Provider: "codex", Email: "b@example.test"},
			{ID: "c", Provider: "claude", Email: "c@example.test", CredentialRef: "PRIVATE-CLAUDE", AuthID: "PRIVATE-AUTH-C"},
		},
		Primaries: []domain.ProviderPrimary{{Provider: "codex", PrimaryID: "a"}, {Provider: "claude", PrimaryID: "c"}},
		Routes:    []domain.ProviderSessionRoute{{SessionID: "s1", Provider: "codex", AccountID: "a", TicketHash: "PRIVATE-TICKET"}, {SessionID: "s2", Provider: "claude", AccountID: "c"}, {SessionID: "waiting", Provider: "codex"}},
	}, recovery: true}
	out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "GET", "/provider-accounts", "")
	if out.Code != 200 {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
	for _, secret := range []string{"PRIVATE-FILE", "PRIVATE-AUTH", "PRIVATE-CLAUDE", "PRIVATE-TICKET", "credential_ref", "auth_id", "ticket_hash", "revision", "pending"} {
		if strings.Contains(out.Body.String(), secret) {
			t.Errorf("inventory leaked %q", secret)
		}
	}
	var data controllers.ProviderAccountsResponse
	if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if !data.RecoveryRequired || len(data.Accounts) != 3 || len(data.Defaults) != 2 {
		t.Fatalf("inventory=%+v", data)
	}
	if !data.Accounts[0].Primary || !data.Accounts[0].SignedIn || !reflect.DeepEqual(data.Accounts[0].Sessions, []string{"s1"}) {
		t.Fatalf("a=%+v", data.Accounts[0])
	}
	if data.Accounts[1].SignedIn || data.Accounts[1].Primary || data.Accounts[1].Sessions == nil {
		t.Fatalf("signed out=%+v", data.Accounts[1])
	}
	if !data.Accounts[2].Primary || !reflect.DeepEqual(data.Accounts[2].Sessions, []string{"s2"}) {
		t.Fatalf("claude=%+v", data.Accounts[2])
	}
}

type signInHTTPFake struct {
	*accountHTTPFake
	failed map[string]bool
	forced []bool
}

func (f *signInHTTPFake) AccountSignInFailures(_ context.Context, _ []domain.ProviderAccount, force bool) map[string]bool {
	f.forced = append(f.forced, force)
	return f.failed
}

func TestProviderAccountsHTTPMarksAccountsWhoseSignInIsNoLongerAccepted(t *testing.T) {
	base := &accountHTTPFake{state: domain.ProviderAccountState{Accounts: []domain.ProviderAccount{
		{ID: "a", Provider: "codex", Email: "a@example.test", CredentialRef: "PRIVATE", AuthID: "PRIVATE-AUTH"},
		{ID: "b", Provider: "codex", Email: "b@example.test", CredentialRef: "PRIVATE-B", AuthID: "PRIVATE-AUTH-B"},
	}}}
	f := &signInHTTPFake{accountHTTPFake: base, failed: map[string]bool{"a": true}}
	controller := &controllers.ProviderAccountsController{Svc: f}
	out := accountHTTPRequest(t, controller, "GET", "/provider-accounts?refresh=true", "")
	var response controllers.ProviderAccountsResponse
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil || out.Code != 200 {
		t.Fatalf("status=%d err=%v body=%s", out.Code, err, out.Body.String())
	}
	if !response.Accounts[0].SignInRequired || !response.Accounts[0].SignedIn || response.Accounts[1].SignInRequired {
		t.Fatalf("accounts=%+v", response.Accounts)
	}
	accountHTTPRequest(t, controller, "GET", "/provider-accounts", "")
	if len(f.forced) != 2 || !f.forced[0] || f.forced[1] {
		t.Fatalf("only an explicit refresh should re-read the helper now: %v", f.forced)
	}
}

func TestProviderAccountsHTTPIncludesSafeUsageSummary(t *testing.T) {
	base := &accountHTTPFake{state: domain.ProviderAccountState{Accounts: []domain.ProviderAccount{{ID: "a", Provider: "codex", Email: "a@example.test", CredentialRef: "PRIVATE", AuthID: "PRIVATE-AUTH"}}}}
	f := &usageHTTPFake{accountHTTPFake: base, usage: map[string]domain.ProviderAccountUsage{"a": {Status: "available", Plan: "Pro", Windows: []domain.ProviderAccountUsageWindow{{Name: "5 hour", RemainingFraction: 0.75, ResetTime: "2030-01-01T00:00:00Z"}}}}}
	out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "GET", "/provider-accounts", "")
	if out.Code != 200 {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
	var response controllers.ProviderAccountsResponse
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Accounts[0].Usage == nil || response.Accounts[0].Usage.Plan != "Pro" || response.Accounts[0].Usage.Windows[0].RemainingFraction != 0.75 {
		t.Fatalf("usage=%+v", response.Accounts[0].Usage)
	}
	if strings.Contains(out.Body.String(), "PRIVATE") {
		t.Fatal("private account data leaked through usage response")
	}
}

func TestProviderAccountsHTTPCanReturnCatalogueWithoutWaitingForUsage(t *testing.T) {
	base := &accountHTTPFake{state: domain.ProviderAccountState{Accounts: []domain.ProviderAccount{{ID: "a", Provider: "codex", Email: "a@example.test", CredentialRef: "PRIVATE", AuthID: "PRIVATE-AUTH"}}}}
	f := &usageHTTPFake{accountHTTPFake: base, usage: map[string]domain.ProviderAccountUsage{"a": {Status: "available", Plan: "Pro"}}}
	out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "GET", "/provider-accounts?includeUsage=false", "")
	if out.Code != 200 {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
	var response controllers.ProviderAccountsResponse
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 || response.Accounts[0].Usage != nil {
		t.Fatalf("usage lookup was not skipped: calls=%d usage=%+v", f.calls, response.Accounts[0].Usage)
	}
}
func TestProviderAccountsHTTPEmptyAndAdoptedProvider(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-primary", true: "managed-without-account"}[adopted], func(t *testing.T) {
			f := &accountHTTPFake{}
			if adopted {
				f.state.Primaries = []domain.ProviderPrimary{{Provider: "codex"}}
			}
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "GET", "/provider-accounts", "")
			var data controllers.ProviderAccountsResponse
			if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if out.Code != 200 || data.Accounts == nil || len(data.Accounts) != 0 || len(data.Defaults) != 2 {
				t.Fatalf("status=%d inventory=%+v", out.Code, data)
			}
			if !data.Defaults[0].Managed || data.Defaults[0].PrimaryID != "" || !data.Defaults[1].Managed {
				t.Fatalf("defaults=%+v", data.Defaults)
			}
		})
	}
}
func TestProviderAccountsHTTPMutationsCarryExactChoice(t *testing.T) {
	cases := []struct {
		name, method, path, body, call, replacement string
		signOut                                     bool
	}{
		{"primary", "PUT", "/provider-accounts/account-two/primary", "", "primary:account-two", "", false},
		{"signout", "POST", "/provider-accounts/account-two/sign-out", `{"replacementPrimaryId":"account-one"}`, "remove:account-two", "account-one", true},
		{"remove", "DELETE", "/provider-accounts/account-two", `{"replacementPrimaryId":"account-one"}`, "remove:account-two", "account-one", false},
		{"remove-empty-body", "DELETE", "/provider-accounts/account-two", "", "remove:account-two", "", false},
		{"switch", "PUT", "/sessions/my-session/provider-account", `{"accountId":"account-two"}`, "switch:my-session:account-two", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &accountHTTPFake{managed: true, route: domain.ProviderSessionRoute{SessionID: "my-session", Provider: "codex", AccountID: "account-two"}}
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, tc.method, tc.path, tc.body)
			if out.Code != 200 || len(f.calls) == 0 || f.calls[0] != tc.call || f.replacement != tc.replacement || f.signOut != tc.signOut {
				t.Fatalf("status=%d calls=%v replacement=%s signOut=%v body=%s", out.Code, f.calls, f.replacement, f.signOut, out.Body.String())
			}
		})
	}
}

func TestProviderAccountsHTTPRenamesWithoutExposingCredentials(t *testing.T) {
	f := &accountHTTPFake{}
	out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "PATCH", "/provider-accounts/account-one", `{"displayName":"Work Codex"}`)
	if out.Code != 200 || f.renameID != "account-one" || f.renameName != "Work Codex" {
		t.Fatalf("status=%d id=%s name=%s body=%s", out.Code, f.renameID, f.renameName, out.Body.String())
	}
}
func TestProviderAccountsHTTPValidationDoesNotMutate(t *testing.T) {
	cases := []struct{ method, path, body string }{
		{"POST", "/provider-accounts/a/sign-out", `{"force":true}`},
		{"DELETE", "/provider-accounts/a", `{`},
		{"PUT", "/sessions/s/provider-account", `{}`},
		{"PUT", "/sessions/s/provider-account", `{"accountId":"  "}`},
		{"PUT", "/sessions/s/provider-account", `{"accountId":"a","force":true}`},
		{"PUT", "/sessions/s/provider-account", `{"accountId":"a"} {"accountId":"b"}`},
		{"POST", "/provider-accounts/login", `{"provider":"gemini"}`},
		{"POST", "/provider-accounts/login", `{"provider":"codex","token":"secret"}`},
		{"POST", "/provider-accounts/login", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			f := &accountHTTPFake{}
			l := &loginHTTPFake{}
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f, Login: l}, tc.method, tc.path, tc.body)
			if out.Code != 400 {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
			if len(f.calls) > 0 || len(l.calls) > 0 {
				t.Fatalf("invalid request executed: %v %v", f.calls, l.calls)
			}
		})
	}
}
func TestProviderAccountsHTTPErrorEnvelopes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ports.ErrProviderAccountBusy, 409, "PROVIDER_ACCOUNT_IN_USE"},
		{ports.ErrProviderPrimaryRequired, 409, "PROVIDER_PRIMARY_REQUIRED"},
		{ports.ErrProviderAccountUnknown, 404, "PROVIDER_ACCOUNT_NOT_FOUND"},
		{ports.ErrProviderLoginRequired, 409, "PROVIDER_LOGIN_REQUIRED"},
		{ports.ErrProviderAccountIncompatible, 400, "PROVIDER_ACCOUNT_INCOMPATIBLE"},
		{ports.ErrProviderAccountRecovery, 409, "PROVIDER_ACCOUNT_RECOVERY_REQUIRED"},
		{errors.New("disk unavailable"), 500, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			f := &accountHTTPFake{err: tc.err}
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "PUT", "/provider-accounts/a/primary", "")
			if out.Code != tc.status || !strings.Contains(out.Body.String(), tc.code) {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
		})
	}
}
func TestProviderAccountsHTTPSessionAccountStates(t *testing.T) {
	cases := []struct {
		name    string
		managed bool
		id      string
	}{
		{"older-native", false, ""}, {"assigned", true, "a"}, {"waiting-for-login", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &accountHTTPFake{managed: tc.managed, route: domain.ProviderSessionRoute{Provider: "codex", AccountID: tc.id, TicketHash: "secret"}}
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "GET", "/sessions/s/provider-account", "")
			var data controllers.SessionProviderAccountResponse
			if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if out.Code != 200 || data.Managed != tc.managed || data.AccountID != tc.id || data.LoginRequired != (tc.managed && tc.id == "") {
				t.Fatalf("status=%d route=%+v", out.Code, data)
			}
			if strings.Contains(out.Body.String(), "secret") {
				t.Fatal("ticket escaped session API")
			}
		})
	}
}
func TestProviderAccountsHTTPLoginFlowAndSafeResponse(t *testing.T) {
	cases := []struct {
		method, path, body, call string
		status                   int
	}{
		{"POST", "/provider-accounts/login", `{"provider":"codex","accountId":"saved"}`, "start:codex:saved", 200},
		{"POST", "/provider-accounts/login", `{"provider":"claude"}`, "start:claude:", 200},
		{"GET", "/provider-accounts/login/login-1", "", "status:login-1", 200},
		{"DELETE", "/provider-accounts/login/login-1", "", "cancel:login-1", 204},
	}
	for _, tc := range cases {
		t.Run(tc.call, func(t *testing.T) {
			l := &loginHTTPFake{login: ports.ProviderLogin{ID: "login-1", Provider: "codex", State: "PRIVATE-OAUTH-STATE", URL: "https://provider.example/login", Status: "waiting", AccountID: "saved"}}
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: &accountHTTPFake{}, Login: l}, tc.method, tc.path, tc.body)
			if out.Code != tc.status || !reflect.DeepEqual(l.calls, []string{tc.call}) {
				t.Fatalf("status=%d calls=%v body=%s", out.Code, l.calls, out.Body.String())
			}
			if strings.Contains(out.Body.String(), "PRIVATE-OAUTH-STATE") || strings.Contains(out.Body.String(), `"state"`) {
				t.Fatal("OAuth relay state leaked")
			}
			if tc.status == 204 && out.Body.Len() != 0 {
				t.Fatal("cancel response should be empty")
			}
		})
	}
}
func TestProviderAccountsHTTPUnavailableAlwaysReplies(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{"GET", "/provider-accounts", ""}, {"POST", "/provider-accounts/login", `{"provider":"codex"}`}, {"GET", "/provider-accounts/login/l", ""}, {"DELETE", "/provider-accounts/login/l", ""}, {"PUT", "/provider-accounts/a/primary", ""}, {"POST", "/provider-accounts/a/sign-out", ""}, {"DELETE", "/provider-accounts/a", ""}, {"GET", "/sessions/s/provider-account", ""}, {"PUT", "/sessions/s/provider-account", `{"accountId":"a"}`},
	}
	for _, tc := range routes {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{}, tc.method, tc.path, tc.body)
			if out.Code != 503 || out.Body.Len() == 0 {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
		})
	}
	for _, tc := range routes[1:4] {
		t.Run("login-only-unavailable"+tc.method, func(t *testing.T) {
			out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: &accountHTTPFake{}}, tc.method, tc.path, tc.body)
			if out.Code != 503 {
				t.Fatalf("status=%d", out.Code)
			}
		})
	}
}

func TestProviderAccountsHTTPPrimaryChoiceIsForwarded(t *testing.T) {
	f := &accountHTTPFake{}
	out := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: f}, "PUT", "/provider-accounts/a/primary", `{"moveExisting":true}`)
	if out.Code != 200 || len(f.calls) != 1 || f.calls[0] != "primary:a" || f.moveExisting == nil || !*f.moveExisting {
		t.Fatalf("status=%d calls=%v moveExisting=%v", out.Code, f.calls, f.moveExisting)
	}
	legacy := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: &accountHTTPFake{}}, "GET", "/provider-accounts", "")
	if legacy.Code != 200 || strings.Contains(legacy.Body.String(), "codexRequestSwitching") {
		t.Fatal("routing capability flag leaked into inventory")
	}
}
