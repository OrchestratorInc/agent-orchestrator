package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/provideraccounts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type flowProxy struct {
	routes                ports.ProviderRouteSnapshot
	deleted               []string
	failApply, failDelete error
}

func (p *flowProxy) ApplyRoutes(_ context.Context, next ports.ProviderRouteSnapshot) error {
	if p.failApply != nil {
		return p.failApply
	}
	if next.Revision != p.routes.Revision+1 && !reflect.DeepEqual(next, p.routes) {
		return ports.ErrProviderAccountRecovery
	}
	p.routes = next
	return nil
}
func (p *flowProxy) DeleteCredential(_ context.Context, ref string) error {
	if p.failDelete != nil {
		return p.failDelete
	}
	p.deleted = append(p.deleted, ref)
	return nil
}

type flowGuard struct {
	busy       map[domain.SessionID]bool
	admissions [][]domain.SessionID
	releases   int
}

func (g *flowGuard) AcquireAccountMutation(_ context.Context, ids []domain.SessionID) (func(), error) {
	for _, id := range ids {
		if g.busy[id] {
			return nil, ports.ErrProviderAccountBusy
		}
	}
	g.admissions = append(g.admissions, append([]domain.SessionID(nil), ids...))
	return func() { g.releases++ }, nil
}

type accountHTTPFlow struct {
	service    *provideraccounts.Service
	controller *controllers.ProviderAccountsController
	proxy      *flowProxy
	guard      *flowGuard
}

func newAccountHTTPFlow(t *testing.T) accountHTTPFlow {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	proxy := &flowProxy{}
	guard := &flowGuard{busy: map[domain.SessionID]bool{}}
	n := 0
	service := provideraccounts.New(store, proxy, guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:4321", func() string { n++; return fmt.Sprintf("account-%d", n) })
	return accountHTTPFlow{service: service, controller: &controllers.ProviderAccountsController{Svc: service}, proxy: proxy, guard: guard}
}
func (f accountHTTPFlow) add(t *testing.T, provider, email string) string {
	t.Helper()
	id, err := f.service.RecordCredential(context.Background(), ports.VerifiedProviderLogin{Provider: provider, Email: email, CredentialRef: email + ".json", AuthID: provider + ":" + email}, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f accountHTTPFlow) assign(t *testing.T, id domain.SessionID, harness domain.AgentHarness, account string) {
	t.Helper()
	if err := f.service.AssignAccount(context.Background(), id, harness, account); err != nil {
		t.Fatal(err)
	}
}
func (f accountHTTPFlow) catalogue(t *testing.T) controllers.ProviderAccountsResponse {
	t.Helper()
	out := accountHTTPRequest(t, f.controller, http.MethodGet, "/provider-accounts", "")
	if out.Code != http.StatusOK {
		t.Fatalf("catalogue status=%d body=%s", out.Code, out.Body.String())
	}
	var result controllers.ProviderAccountsResponse
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// Usage is a best-effort read and is represented by a fresh pointer on each
	// catalogue read; these flow assertions compare durable account facts only.
	for i := range result.Accounts {
		result.Accounts[i].Usage = nil
	}
	for _, private := range []string{"credential_ref", "auth_id", "ticket_hash", "access_token", "refresh_token", ".json", "codex:alice", "claude:clara"} {
		if strings.Contains(out.Body.String(), private) {
			t.Fatalf("public catalogue exposed %q", private)
		}
	}
	return result
}
func (f accountHTTPFlow) route(t *testing.T, id, account string, loginRequired bool) {
	t.Helper()
	out := accountHTTPRequest(t, f.controller, http.MethodGet, "/sessions/"+id+"/provider-account", "")
	if out.Code != http.StatusOK {
		t.Fatalf("session read status=%d body=%s", out.Code, out.Body.String())
	}
	var result controllers.SessionProviderAccountResponse
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Managed || result.AccountID != account || result.LoginRequired != loginRequired {
		t.Fatalf("session route=%+v", result)
	}
	if strings.Contains(out.Body.String(), "ticket") || strings.Contains(out.Body.String(), "auth_id") {
		t.Fatal("session route API exposed private capability")
	}
}
func TestProviderAccountsHTTPFlowPrimaryPreferenceAndManualSessionSwitch(t *testing.T) {
	f := newAccountHTTPFlow(t)
	ctx := context.Background()
	empty := f.catalogue(t)
	if len(empty.Accounts) != 0 || len(empty.Defaults) != 2 || !empty.Defaults[0].Managed || !empty.Defaults[1].Managed {
		t.Fatalf("initial catalogue=%+v", empty)
	}
	alice := f.add(t, "codex", "alice@example.test")
	bob := f.add(t, "codex", "bob@example.test")
	clara := f.add(t, "claude", "clara@example.test")
	f.assign(t, "old-codex", domain.HarnessCodex, alice)
	f.assign(t, "existing-claude", domain.HarnessClaudeCode, clara)
	before := f.catalogue(t)
	if len(before.Accounts) != 3 || !before.Accounts[0].Primary || before.Accounts[1].Primary || !before.Accounts[2].Primary {
		t.Fatalf("primary catalogue=%+v", before)
	}
	oldEnv, err := f.service.LaunchAccountEnv(ctx, "old-codex")
	if err != nil {
		t.Fatal(err)
	}
	f.guard.busy["old-codex"] = true
	out := accountHTTPRequest(t, f.controller, http.MethodPut, "/provider-accounts/"+bob+"/primary", "")
	if out.Code != http.StatusOK {
		t.Fatalf("primary status=%d body=%s", out.Code, out.Body.String())
	}
	f.route(t, "old-codex", alice, false)
	f.route(t, "existing-claude", clara, false)
	chosen, managed, err := f.service.ResolveAccount(ctx, domain.HarnessCodex, "")
	if err != nil || !managed || chosen != bob {
		t.Fatalf("new session choice=%s managed=%v err=%v", chosen, managed, err)
	}
	f.assign(t, "new-codex", domain.HarnessCodex, chosen)
	f.route(t, "new-codex", bob, false)
	out = accountHTTPRequest(t, f.controller, http.MethodPut, "/sessions/old-codex/provider-account", `{"accountId":"`+bob+`"}`)
	if out.Code != http.StatusOK {
		t.Fatalf("request-boundary switch status=%d body=%s", out.Code, out.Body.String())
	}
	f.route(t, "old-codex", bob, false)
	f.guard.busy["old-codex"] = false
	out = accountHTTPRequest(t, f.controller, http.MethodPut, "/sessions/old-codex/provider-account", `{"accountId":"`+bob+`"}`)
	if out.Code != http.StatusOK {
		t.Fatalf("manual switch status=%d body=%s", out.Code, out.Body.String())
	}
	f.route(t, "old-codex", bob, false)
	switchedEnv, err := f.service.LaunchAccountEnv(ctx, "old-codex")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oldEnv, switchedEnv) {
		t.Fatal("account change altered existing process endpoint or ticket")
	}
	after := f.catalogue(t)
	if after.Defaults[0].PrimaryID != bob || after.Defaults[1].PrimaryID != clara {
		t.Fatalf("separate defaults=%+v", after.Defaults)
	}
	if !reflect.DeepEqual(after.Accounts[1].Sessions, []string{"old-codex", "new-codex"}) {
		t.Fatalf("assigned sessions=%v", after.Accounts[1].Sessions)
	}
	if len(f.proxy.deleted) != 0 {
		t.Fatal("preference or manual switch deleted a login")
	}
	if f.guard.releases != len(f.guard.admissions) {
		t.Fatal("HTTP operations retained account guards")
	}
}

func TestProviderAccountsHTTPFlowPrimarySignOutRequiresReplacement(t *testing.T) {
	f := newAccountHTTPFlow(t)
	alice := f.add(t, "codex", "alice@example.test")
	bob := f.add(t, "codex", "bob@example.test")
	clara := f.add(t, "claude", "clara@example.test")
	f.assign(t, "uses-alice", domain.HarnessCodex, alice)
	f.assign(t, "uses-bob", domain.HarnessCodex, bob)
	before := f.catalogue(t)
	out := accountHTTPRequest(t, f.controller, http.MethodPost, "/provider-accounts/"+alice+"/sign-out", `{}`)
	if out.Code != http.StatusConflict || !strings.Contains(out.Body.String(), "PROVIDER_PRIMARY_REQUIRED") {
		t.Fatalf("missing replacement=%d %s", out.Code, out.Body.String())
	}
	if !reflect.DeepEqual(before, f.catalogue(t)) {
		t.Fatal("replacement refusal changed catalogue")
	}
	if len(f.proxy.deleted) != 0 {
		t.Fatal("refused sign-out erased credentials")
	}
	out = accountHTTPRequest(t, f.controller, http.MethodPost, "/provider-accounts/"+alice+"/sign-out", `{"replacementPrimaryId":"`+clara+`"}`)
	if out.Code != http.StatusBadRequest || !strings.Contains(out.Body.String(), "PROVIDER_ACCOUNT_INCOMPATIBLE") {
		t.Fatalf("cross-provider replacement=%d %s", out.Code, out.Body.String())
	}
	out = accountHTTPRequest(t, f.controller, http.MethodPost, "/provider-accounts/"+alice+"/sign-out", `{"replacementPrimaryId":"`+bob+`"}`)
	if out.Code != http.StatusOK {
		t.Fatalf("primary sign-out=%d %s", out.Code, out.Body.String())
	}
	f.route(t, "uses-alice", bob, false)
	f.route(t, "uses-bob", bob, false)
	catalogue := f.catalogue(t)
	if len(catalogue.Accounts) != 3 || catalogue.Accounts[0].SignedIn || catalogue.Accounts[0].Primary {
		t.Fatalf("retained signed-out identity=%+v", catalogue.Accounts)
	}
	if catalogue.Defaults[0].PrimaryID != bob {
		t.Fatalf("replacement=%+v", catalogue.Defaults)
	}
	if !reflect.DeepEqual(f.proxy.deleted, []string{"alice@example.test.json"}) {
		t.Fatalf("credential removal=%v", f.proxy.deleted)
	}
	out = accountHTTPRequest(t, f.controller, http.MethodDelete, "/provider-accounts/"+alice, "")
	if out.Code != http.StatusOK {
		t.Fatalf("remove retained entry=%d %s", out.Code, out.Body.String())
	}
	catalogue = f.catalogue(t)
	if len(catalogue.Accounts) != 2 {
		t.Fatalf("removed retained account count=%d", len(catalogue.Accounts))
	}
	for _, a := range catalogue.Accounts {
		if a.ID == alice {
			t.Fatal("removed signed-out entry remained")
		}
	}
	if len(f.proxy.deleted) != 1 {
		t.Fatal("removing signed-out identity repeated credential deletion")
	}
}
func TestProviderAccountsHTTPFlowLastSignOutAndNewLoginRestoresWaitingRoutes(t *testing.T) {
	f := newAccountHTTPFlow(t)
	alice := f.add(t, "codex", "alice@example.test")
	clara := f.add(t, "claude", "clara@example.test")
	f.assign(t, "codex-one", domain.HarnessCodex, alice)
	f.assign(t, "codex-two", domain.HarnessCodex, alice)
	f.assign(t, "claude-one", domain.HarnessClaudeCode, clara)
	original, err := f.service.LaunchAccountEnv(context.Background(), "codex-one")
	if err != nil {
		t.Fatal(err)
	}
	out := accountHTTPRequest(t, f.controller, http.MethodDelete, "/provider-accounts/"+alice, "")
	if out.Code != http.StatusOK {
		t.Fatalf("last removal=%d %s", out.Code, out.Body.String())
	}
	f.route(t, "codex-one", "", true)
	f.route(t, "codex-two", "", true)
	f.route(t, "claude-one", clara, false)
	catalogue := f.catalogue(t)
	if len(catalogue.Accounts) != 1 || catalogue.Defaults[0].PrimaryID != "" || !catalogue.Defaults[0].Managed {
		t.Fatalf("no-account catalogue=%+v", catalogue)
	}
	id, managed, err := f.service.ResolveAccount(context.Background(), domain.HarnessCodex, "")
	if id != "" || !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("no-account spawn=%s %v %v", id, managed, err)
	}
	native, managed, err := f.service.SessionAccount(context.Background(), "older-native")
	if err != nil || managed || native.AccountID != "" {
		t.Fatalf("older native was converted=%+v %v %v", native, managed, err)
	}
	bob := f.add(t, "codex", "bob@example.test")
	f.route(t, "codex-one", bob, false)
	f.route(t, "codex-two", bob, false)
	f.route(t, "claude-one", clara, false)
	recovered, err := f.service.LaunchAccountEnv(context.Background(), "codex-one")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, recovered) {
		t.Fatal("login recovery changed process environment")
	}
	catalogue = f.catalogue(t)
	if catalogue.Defaults[0].PrimaryID != bob || catalogue.Defaults[1].PrimaryID != clara {
		t.Fatalf("recovered defaults=%+v", catalogue.Defaults)
	}
	if !reflect.DeepEqual(catalogue.Accounts[1].Sessions, []string{"codex-one", "codex-two"}) {
		t.Fatalf("recovered waiting sessions=%v", catalogue.Accounts[1].Sessions)
	}
}
func TestProviderAccountsHTTPFlowCredentialCleanupFailureIsNeverReportedAsSuccess(t *testing.T) {
	f := newAccountHTTPFlow(t)
	alice := f.add(t, "codex", "alice@example.test")
	bob := f.add(t, "codex", "bob@example.test")
	f.assign(t, "uses-bob", domain.HarnessCodex, bob)
	f.proxy.failDelete = errors.New("credential disk is unavailable")
	out := accountHTTPRequest(t, f.controller, http.MethodDelete, "/provider-accounts/"+bob, "")
	if out.Code != http.StatusInternalServerError {
		t.Fatalf("cleanup status=%d body=%s", out.Code, out.Body.String())
	}
	f.route(t, "uses-bob", alice, false)
	catalogue := f.catalogue(t)
	if !catalogue.RecoveryRequired {
		t.Fatal("failed credential cleanup was hidden")
	}
	if len(f.proxy.deleted) != 0 {
		t.Fatal("fake cleanup failure recorded deletion")
	}
	_, _, err := f.service.ResolveAccount(context.Background(), domain.HarnessCodex, "")
	if !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("pending cleanup admitted new spawn: %v", err)
	}
	f.proxy.failDelete = nil
	out = accountHTTPRequest(t, f.controller, http.MethodDelete, "/provider-accounts/"+bob, "")
	// The pending operation completes before the new action is evaluated. The
	// original removed identity is consequently absent and this retry is 404.
	if out.Code != http.StatusNotFound {
		t.Fatalf("replayed removed identity status=%d body=%s", out.Code, out.Body.String())
	}
	catalogue = f.catalogue(t)
	if catalogue.RecoveryRequired {
		t.Fatal("credential cleanup did not recover")
	}
	if !reflect.DeepEqual(f.proxy.deleted, []string{"bob@example.test.json"}) {
		t.Fatalf("recovery deletion=%v", f.proxy.deleted)
	}
	f.route(t, "uses-bob", alice, false)
}
func TestProviderAccountsHTTPFlowBusyRemovalDoesNotApplyOnLaterReads(t *testing.T) {
	f := newAccountHTTPFlow(t)
	alice := f.add(t, "codex", "alice@example.test")
	bob := f.add(t, "codex", "bob@example.test")
	f.assign(t, "busy", domain.HarnessCodex, bob)
	before := f.catalogue(t)
	revision := f.proxy.routes.Revision
	f.guard.busy["busy"] = true
	out := accountHTTPRequest(t, f.controller, http.MethodDelete, "/provider-accounts/"+bob, "")
	if out.Code != http.StatusConflict {
		t.Fatalf("busy removal=%d %s", out.Code, out.Body.String())
	}
	f.guard.busy["busy"] = false
	after := f.catalogue(t)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refused deletion happened when a later read saw idle")
	}
	if f.proxy.routes.Revision != revision || len(f.proxy.deleted) != 0 {
		t.Fatal("busy operation published or deleted later")
	}
	f.route(t, "busy", bob, false)
	if err := f.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.route(t, "busy", bob, false)
	out = accountHTTPRequest(t, f.controller, http.MethodDelete, "/provider-accounts/"+bob, "")
	if out.Code != http.StatusOK {
		t.Fatalf("user retry=%d %s", out.Code, out.Body.String())
	}
	f.route(t, "busy", alice, false)
}
func TestProviderAccountsHTTPFlowNativeAndWrongProviderSwitchesAreRejected(t *testing.T) {
	f := newAccountHTTPFlow(t)
	alice := f.add(t, "codex", "alice@example.test")
	clara := f.add(t, "claude", "clara@example.test")
	f.assign(t, "codex-session", domain.HarnessCodex, alice)
	before := f.catalogue(t)
	for _, tc := range []struct{ session, target string }{
		{"codex-session", clara},
		{"old-native", alice},
	} {
		out := accountHTTPRequest(t, f.controller, http.MethodPut, "/sessions/"+tc.session+"/provider-account", `{"accountId":"`+tc.target+`"}`)
		if out.Code != http.StatusBadRequest || !strings.Contains(out.Body.String(), "PROVIDER_ACCOUNT_INCOMPATIBLE") {
			t.Fatalf("switch %s status=%d body=%s", tc.session, out.Code, out.Body.String())
		}
	}
	f.route(t, "codex-session", alice, false)
	if !reflect.DeepEqual(before, f.catalogue(t)) {
		t.Fatal("rejected route change wrote account catalogue")
	}
	if len(f.proxy.deleted) != 0 {
		t.Fatal("wrong-provider switch deleted credentials")
	}
}
