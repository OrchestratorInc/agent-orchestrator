package provideraccounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var errTestFailure = errors.New("injected failure")

type memoryStore struct {
	mu      sync.Mutex
	state   domain.ProviderAccountState
	pending *domain.ProviderAccountIntent
	fail    string
}

func clone[T any](v T) T {
	data, _ := json.Marshal(v)
	var result T
	_ = json.Unmarshal(data, &result)
	return result
}
func (m *memoryStore) LoadProviderAccountState(context.Context) (domain.ProviderAccountState, *domain.ProviderAccountIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail == "load" {
		return domain.ProviderAccountState{}, nil, errTestFailure
	}
	return clone(m.state), clone(m.pending), nil
}
func (m *memoryStore) SaveProviderAccountIntent(_ context.Context, revision int64, intent domain.ProviderAccountIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail == "save" {
		return errTestFailure
	}
	if m.pending != nil || m.state.Revision != revision {
		return ports.ErrProviderAccountRecovery
	}
	m.pending = clone(&intent)
	return nil
}
func (m *memoryStore) CommitProviderAccountIntent(_ context.Context, revision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail == "commit" {
		return errTestFailure
	}
	if m.pending == nil || m.state.Revision != revision {
		return ports.ErrProviderAccountRecovery
	}
	m.state = clone(m.pending.Next)
	return nil
}
func (m *memoryStore) FinishProviderAccountIntent(_ context.Context, revision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail == "finish" {
		return errTestFailure
	}
	if m.pending == nil || m.state.Revision != revision {
		return ports.ErrProviderAccountRecovery
	}
	m.pending = nil
	return nil
}

type fakeProxy struct {
	mu       sync.Mutex
	snapshot ports.ProviderRouteSnapshot
	deleted  []string
	fail     string
	applied  []ports.ProviderRouteSnapshot
}

type usageProxy struct {
	*fakeProxy
	usageCalls int
	usage      domain.ProviderAccountUsage
	usageErr   error
}

func (p *usageProxy) FetchAccountUsage(context.Context, string, string, string) (domain.ProviderAccountUsage, error) {
	p.usageCalls++
	return p.usage, p.usageErr
}

func (p *fakeProxy) ApplyRoutes(_ context.Context, s ports.ProviderRouteSnapshot) error {
	s.RequestBoundary = false
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail == "apply" {
		return errTestFailure
	}
	if s.Revision == p.snapshot.Revision && !reflect.DeepEqual(s, p.snapshot) {
		return ports.ErrProviderAccountRecovery
	}
	if s.Revision < p.snapshot.Revision {
		return ports.ErrProviderAccountRecovery
	}
	p.snapshot = clone(s)
	p.applied = append(p.applied, clone(s))
	return nil
}
func (p *fakeProxy) DeleteCredential(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail == "delete" {
		return errTestFailure
	}
	p.deleted = append(p.deleted, name)
	return nil
}

type fakeGuard struct {
	mu       sync.Mutex
	busy     map[domain.SessionID]bool
	acquired [][]domain.SessionID
	released int
}

func (g *fakeGuard) AcquireAccountMutation(_ context.Context, ids []domain.SessionID) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range ids {
		if g.busy[id] {
			return nil, ports.ErrProviderAccountBusy
		}
	}
	g.acquired = append(g.acquired, append([]domain.SessionID(nil), ids...))
	return func() { g.mu.Lock(); defer g.mu.Unlock(); g.released++ }, nil
}

type accountHarness struct {
	svc   *Service
	store *memoryStore
	proxy *fakeProxy
	guard *fakeGuard
	ctx   context.Context
}

func setupAccounts(t *testing.T) accountHarness {
	t.Helper()
	store := &memoryStore{}
	proxy := &fakeProxy{}
	guard := &fakeGuard{busy: make(map[domain.SessionID]bool)}
	n := 0
	return accountHarness{svc: New(store, proxy, guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { n++; return fmt.Sprintf("account-%d", n) }), store: store, proxy: proxy, guard: guard, ctx: context.Background()}
}
func (h accountHarness) login(t *testing.T, provider, email string) string {
	t.Helper()
	id, err := recordLogin(h.ctx, h.svc, provider, email, email+".json", email+"-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func recordLogin(ctx context.Context, service *Service, provider, email, credentialRef, authID, reloginID string) (string, error) {
	return service.RecordCredential(ctx, ports.VerifiedProviderLogin{Provider: provider, Email: email, CredentialRef: credentialRef, AuthID: authID}, reloginID)
}
func (h accountHarness) assign(t *testing.T, session string, harness domain.AgentHarness, id string) {
	t.Helper()
	if err := h.svc.AssignAccount(h.ctx, domain.SessionID(session), harness, id); err != nil {
		t.Fatal(err)
	}
}
func (h accountHarness) route(t *testing.T, session, want string) {
	t.Helper()
	got, ok, err := h.svc.SessionAccount(h.ctx, domain.SessionID(session))
	if err != nil || !ok || got.AccountID != want {
		t.Fatalf("session=%s route=%+v found=%t err=%v want=%s", session, got, ok, err, want)
	}
}
func TestProviderAccountFirstLoginAndSeparatePrimaries(t *testing.T) {
	h := setupAccounts(t)
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		if id, managed, err := h.svc.ResolveAccount(h.ctx, harness, ""); !managed || !errors.Is(err, ports.ErrProviderLoginRequired) || id != "" {
			t.Fatalf("before setup id=%s managed=%t err=%v", id, managed, err)
		}
	}
	codex := h.login(t, "codex", "alice@example.com")
	claude := h.login(t, "claude", "bob@example.com")
	h.login(t, "codex", "charlie@example.com")
	h.login(t, "claude", "dana@example.com")
	for _, tc := range []struct {
		harness domain.AgentHarness
		want    string
	}{{domain.HarnessCodex, codex}, {domain.HarnessClaudeCode, claude}} {
		id, managed, err := h.svc.ResolveAccount(h.ctx, tc.harness, "")
		if err != nil || !managed || id != tc.want {
			t.Fatalf("resolved=%s managed=%t err=%v", id, managed, err)
		}
	}
}

func TestProviderAccountUsageIsSafeAndCached(t *testing.T) {
	store := &memoryStore{}
	proxy := &usageProxy{fakeProxy: &fakeProxy{}, usage: domain.ProviderAccountUsage{Status: "available", Plan: "Pro", Windows: []domain.ProviderAccountUsageWindow{{RemainingFraction: 0.75}}}}
	svc := New(store, proxy, &fakeGuard{busy: make(map[domain.SessionID]bool)}, []byte("key"), "", func() string { return "account-1" })
	id, err := recordLogin(context.Background(), svc, "codex", "alice@example.test", "alice.json", "auth-1", "")
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := svc.AccountUsages(context.Background(), state.Accounts)
	second := svc.AccountUsages(context.Background(), state.Accounts)
	if proxy.usageCalls != 1 || first[id].Plan != "Pro" || second[id].Windows[0].RemainingFraction != 0.75 {
		t.Fatalf("calls=%d first=%+v second=%+v", proxy.usageCalls, first[id], second[id])
	}
}

func TestProviderAccountUsageKeepsInventoryWhenProviderCannotReport(t *testing.T) {
	store := &memoryStore{}
	proxy := &usageProxy{fakeProxy: &fakeProxy{}, usageErr: errTestFailure}
	svc := New(store, proxy, &fakeGuard{busy: make(map[domain.SessionID]bool)}, []byte("key"), "", func() string { return "account-1" })
	if _, err := recordLogin(context.Background(), svc, "codex", "alice@example.test", "alice.json", "auth-1", ""); err != nil {
		t.Fatal(err)
	}
	state, err := svc.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	usage := svc.AccountUsages(context.Background(), state.Accounts)
	if usage[state.Accounts[0].ID].Status != "unavailable" || usage[state.Accounts[0].ID].Message != "Usage unavailable" {
		t.Fatalf("usage=%+v", usage)
	}
}
func TestProviderAccountPrimaryChangesOnlyNewSessions(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	h.assign(t, "old", domain.HarnessCodex, alice)
	if err := h.svc.SetPrimary(h.ctx, bob); err != nil {
		t.Fatal(err)
	}
	h.route(t, "old", alice)
	selected, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || !managed || selected != bob {
		t.Fatalf("selected=%s managed=%t err=%v", selected, managed, err)
	}
	h.assign(t, "new", domain.HarnessCodex, selected)
	h.route(t, "new", bob)
}

func TestProviderAccountDisplayNameIsGeneratedAndEditable(t *testing.T) {
	h := setupAccounts(t)
	id := h.login(t, "codex", "alice@example.test")
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || state.Accounts[0].DisplayName == "" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	initial := state.Accounts[0].DisplayName
	if err := h.svc.Rename(h.ctx, id, "  Work   Codex "); err != nil {
		t.Fatal(err)
	}
	state, err = h.svc.State(h.ctx)
	if err != nil || state.Accounts[0].DisplayName != "Work Codex" {
		t.Fatalf("renamed state=%+v err=%v", state, err)
	}
	if initial == state.Accounts[0].DisplayName {
		t.Fatalf("rename did not change generated name %q", initial)
	}
	if err := h.svc.Rename(h.ctx, id, "   "); !errors.Is(err, ports.ErrProviderAccountNameInvalid) {
		t.Fatalf("blank rename err=%v", err)
	}
}
func TestProviderAccountExplicitChoiceWinsOverPrimary(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	chosen, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, bob)
	if err != nil || !managed || chosen != bob {
		t.Fatalf("chosen=%s managed=%t err=%v", chosen, managed, err)
	}
	h.assign(t, "explicit", domain.HarnessCodex, chosen)
	h.route(t, "explicit", bob)
	defaultID, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || defaultID != alice {
		t.Fatalf("default=%s err=%v", defaultID, err)
	}
}
func TestProviderAccountManualSwitchPreservesPrimaryAndTicket(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	h.assign(t, "s1", domain.HarnessCodex, alice)
	h.assign(t, "s2", domain.HarnessCodex, alice)
	before, _, err := h.svc.SessionAccount(h.ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.svc.Switch(h.ctx, "s1", bob); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s1", bob)
	h.route(t, "s2", alice)
	after, _, err := h.svc.SessionAccount(h.ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if after.TicketHash != before.TicketHash {
		t.Fatal("switch replaced session ticket")
	}
	primary, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || primary != alice {
		t.Fatalf("primary=%s err=%v", primary, err)
	}
	if h.guard.released != len(h.guard.acquired) || len(h.guard.acquired) != 0 {
		t.Fatalf("guard=%+v", h.guard)
	}
}
func TestProviderAccountSignOutMovesOnlyAssignedSessions(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	claude := h.login(t, "claude", "claude@example.com")
	h.assign(t, "alice-session", domain.HarnessCodex, alice)
	h.assign(t, "bob-1", domain.HarnessCodex, bob)
	h.assign(t, "bob-2", domain.HarnessCodex, bob)
	h.assign(t, "claude-session", domain.HarnessClaudeCode, claude)
	if err := h.svc.Remove(h.ctx, bob, "", true); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"alice-session", "bob-1", "bob-2"} {
		h.route(t, session, alice)
	}
	h.route(t, "claude-session", claude)
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	signedOut, found := account(state, bob)
	if !found || signedOut.CredentialRef != "" || signedOut.AuthID != "" {
		t.Fatalf("signedOut=%+v found=%t", signedOut, found)
	}
	if len(h.proxy.deleted) != 1 || h.proxy.deleted[0] != "bob@example.com.json" {
		t.Fatalf("deleted=%v", h.proxy.deleted)
	}
	if !reflect.DeepEqual(h.guard.acquired, [][]domain.SessionID{{"bob-1", "bob-2"}}) {
		t.Fatalf("guarded=%v", h.guard.acquired)
	}
}
func TestProviderAccountRemoveDeletesCatalogueEntry(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	h.assign(t, "s1", domain.HarnessCodex, bob)
	if err := h.svc.Remove(h.ctx, bob, "", false); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s1", alice)
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := account(state, bob); found {
		t.Fatal("removed entry retained")
	}
}
func TestProviderAccountPrimaryRemovalRequiresReplacement(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	h.assign(t, "s1", domain.HarnessCodex, alice)
	for _, replacement := range []string{"", alice, "missing"} {
		before := clone(h.store.state)
		if err := h.svc.Remove(h.ctx, alice, replacement, false); err == nil {
			t.Fatalf("replacement=%q admitted", replacement)
		}
		if !reflect.DeepEqual(before, h.store.state) || h.store.pending != nil || len(h.proxy.deleted) != 0 {
			t.Fatal("invalid primary removal mutated state")
		}
	}
	if err := h.svc.Remove(h.ctx, alice, bob, false); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s1", bob)
	got, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || got != bob {
		t.Fatalf("primary=%s err=%v", got, err)
	}
}
func TestProviderAccountLastSignOutRequiresLoginThenRecoversWaitingRoutes(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	claude := h.login(t, "claude", "claude@example.com")
	h.assign(t, "c1", domain.HarnessCodex, alice)
	h.assign(t, "c2", domain.HarnessCodex, alice)
	h.assign(t, "a1", domain.HarnessClaudeCode, claude)
	envBefore, err := h.svc.LaunchAccountEnv(h.ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.svc.Remove(h.ctx, alice, "", true); err != nil {
		t.Fatal(err)
	}
	h.route(t, "c1", "")
	h.route(t, "c2", "")
	h.route(t, "a1", claude)
	if _, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("managed=%t err=%v", managed, err)
	}
	if route, ok, err := h.svc.SessionAccount(h.ctx, "native-old"); err != nil || ok || route.AccountID != "" {
		t.Fatalf("older native route=%+v found=%t err=%v", route, ok, err)
	}
	bob := h.login(t, "codex", "bob@example.com")
	h.route(t, "c1", bob)
	h.route(t, "c2", bob)
	h.route(t, "a1", claude)
	envAfter, err := h.svc.LaunchAccountEnv(h.ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envBefore, envAfter) {
		t.Fatal("recovery requires replacing agent environment")
	}
}
func TestProviderAccountReloginRetainsIDWithNewCredentialReference(t *testing.T) {
	h := setupAccounts(t)
	id := h.login(t, "claude", "alice@example.com")
	h.assign(t, "s1", domain.HarnessClaudeCode, id)
	if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
		t.Fatal(err)
	}
	got, err := recordLogin(h.ctx, h.svc, "claude", "ALICE@example.com", "new-file.json", "new-auth-id", id)
	if err != nil || got != id {
		t.Fatalf("id=%s err=%v", got, err)
	}
	h.route(t, "s1", id)
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := account(state, id)
	if !ok || a.CredentialRef != "new-file.json" || a.AuthID != "new-auth-id" || len(state.Accounts) != 1 {
		t.Fatalf("account=%+v count=%d", a, len(state.Accounts))
	}
}
func TestProviderAccountBusyRemovalAndSwitchLeaveCredentialsAndRoutes(t *testing.T) {
	for _, operation := range []string{"remove", "signout", "switch"} {
		t.Run(operation, func(t *testing.T) {
			h := setupAccounts(t)
			alice := h.login(t, "codex", "alice@example.com")
			bob := h.login(t, "codex", "bob@example.com")
			h.assign(t, "s1", domain.HarnessCodex, bob)
			h.guard.busy["s1"] = true
			before := clone(h.store.state)
			var err error
			if operation == "switch" {
				err = h.svc.Switch(h.ctx, "s1", alice)
			} else {
				err = h.svc.Remove(h.ctx, bob, "", operation == "signout")
			}
			if operation == "switch" {
				if err != nil {
					t.Fatalf("Codex request-boundary switch err=%v", err)
				}
				h.route(t, "s1", alice)
				if len(h.proxy.deleted) != 0 {
					t.Fatal("request-boundary switch deleted credentials")
				}
				return
			}
			if !errors.Is(err, ports.ErrProviderAccountBusy) {
				t.Fatalf("err=%v", err)
			}
			if !reflect.DeepEqual(before, h.store.state) || h.store.pending != nil || len(h.proxy.deleted) != 0 {
				t.Fatal("busy operation changed durable facts")
			}
			h.guard.busy["s1"] = false
			if operation == "switch" {
				err = h.svc.Switch(h.ctx, "s1", alice)
			} else {
				err = h.svc.Remove(h.ctx, bob, "", operation == "signout")
			}
			if err != nil {
				t.Fatal(err)
			}
			h.route(t, "s1", alice)
		})
	}
}
func TestProviderAccountWrongProviderAndSignedOutSelectionsDeny(t *testing.T) {
	h := setupAccounts(t)
	codex := h.login(t, "codex", "codex@example.com")
	claude := h.login(t, "claude", "claude@example.com")
	h.assign(t, "s1", domain.HarnessCodex, codex)
	if err := h.svc.Switch(h.ctx, "s1", claude); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("switch err=%v", err)
	}
	if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, claude); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("resolve err=%v", err)
	}
	if err := h.svc.Remove(h.ctx, claude, "", true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetPrimary(h.ctx, claude); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("primary err=%v", err)
	}
	if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, claude); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("signed out resolve err=%v", err)
	}
}
func TestProviderAccountFailureRecoveryAtEveryBoundary(t *testing.T) {
	for _, point := range []string{"save", "apply", "commit", "delete", "finish"} {
		t.Run(point, func(t *testing.T) {
			h := setupAccounts(t)
			alice := h.login(t, "codex", "alice@example.com")
			bob := h.login(t, "codex", "bob@example.com")
			h.assign(t, "s1", domain.HarnessCodex, bob)
			before := clone(h.store.state)
			if point == "apply" || point == "delete" {
				h.proxy.fail = point
			} else {
				h.store.fail = point
			}
			if err := h.svc.Remove(h.ctx, bob, "", false); !errors.Is(err, errTestFailure) {
				t.Fatalf("failure=%v", err)
			}
			if point == "save" {
				if h.store.pending != nil || !reflect.DeepEqual(h.store.state, before) {
					t.Fatal("failed admission changed facts")
				}
			} else if h.store.pending == nil {
				t.Fatal("recoverable operation lost intent")
			}
			if point == "apply" || point == "commit" {
				if !reflect.DeepEqual(h.store.state, before) {
					t.Fatal("effective facts preceded helper acknowledgement")
				}
				if len(h.proxy.deleted) != 0 {
					t.Fatal("deleted credential before route commit")
				}
			}
			h.proxy.fail = ""
			h.store.fail = ""
			// A replacement service has no memory of the interrupted operation.
			replacement := New(h.store, h.proxy, h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { return "unused" })
			if point == "save" {
				if err := replacement.Remove(h.ctx, bob, "", false); err != nil {
					t.Fatal(err)
				}
			} else if err := replacement.Recover(h.ctx); err != nil {
				t.Fatal(err)
			}
			h.route(t, "s1", alice)
			if h.store.pending != nil {
				t.Fatal("completed recovery retained intent")
			}
			if _, ok := account(h.store.state, bob); ok {
				t.Fatal("removed account retained after recovery")
			}
			if len(h.proxy.deleted) == 0 {
				t.Fatal("recovery skipped credential deletion")
			}
		})
	}
}
func TestProviderAccountPendingOperationBlocksNewAssignments(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.com")
	bob := h.login(t, "codex", "bob@example.com")
	h.assign(t, "s1", domain.HarnessCodex, bob)
	h.proxy.fail = "apply"
	if err := h.svc.Remove(h.ctx, bob, "", false); err == nil {
		t.Fatal("failure not observed")
	}
	if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("err=%v", err)
	}
	if err := h.svc.AssignAccount(h.ctx, "s2", domain.HarnessCodex, alice); err == nil {
		t.Fatal("new assignment bypassed failed recovery")
	}
}
func TestProviderAccountLaunchEnvironmentUsesPrivateStableTicket(t *testing.T) {
	h := setupAccounts(t)
	codex := h.login(t, "codex", "codex@example.com")
	claude := h.login(t, "claude", "claude@example.com")
	h.assign(t, "c1", domain.HarnessCodex, codex)
	h.assign(t, "c2", domain.HarnessCodex, codex)
	h.assign(t, "a1", domain.HarnessClaudeCode, claude)
	c1, err := h.svc.LaunchAccountEnv(h.ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := h.svc.LaunchAccountEnv(h.ctx, "c2")
	if err != nil {
		t.Fatal(err)
	}
	a1, err := h.svc.LaunchAccountEnv(h.ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if c1["AO_PROXY_TICKET"] == c2["AO_PROXY_TICKET"] || len(c1["AO_PROXY_TICKET"]) != 64 {
		t.Fatal("session tickets not distinct private capabilities")
	}
	if c1["AO_PROXY_ENDPOINT"] != "http://127.0.0.1:1234" || a1["ANTHROPIC_BASE_URL"] != c1["AO_PROXY_ENDPOINT"] {
		t.Fatal("wrong helper endpoint")
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		if value, ok := a1[key]; !ok || value != "" {
			t.Fatalf("ambient credential bypass %s=%q present=%t", key, value, ok)
		}
	}
	bytes, _ := json.Marshal(h.store.state)
	if strings.Contains(string(bytes), c1["AO_PROXY_TICKET"]) || strings.Contains(string(bytes), a1["ANTHROPIC_AUTH_TOKEN"]) {
		t.Fatal("raw capability persisted in account catalogue")
	}
}
func TestProviderAccountOlderNativeSessionUnchanged(t *testing.T) {
	h := setupAccounts(t)
	h.login(t, "codex", "alice@example.com")
	if env, err := h.svc.LaunchAccountEnv(h.ctx, "old-native"); err != nil || env != nil {
		t.Fatalf("env=%v err=%v", env, err)
	}
	if err := h.svc.Switch(h.ctx, "old-native", "account-1"); err == nil {
		t.Fatal("converted older native session")
	}
}
func TestProviderAccountCancelledGateAdmission(t *testing.T) {
	h := setupAccounts(t)
	h.svc.gate <- struct{}{}
	ctx, cancel := context.WithCancel(h.ctx)
	cancel()
	if err := h.svc.SetPrimary(ctx, "unknown"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	<-h.svc.gate
	if h.store.pending != nil {
		t.Fatal("cancelled caller admitted intent")
	}
}

func TestProviderAccountClaudeDefaultCanMoveExistingRoutes(t *testing.T) {
	h := setupAccounts(t)
	a := h.login(t, "claude", "alice@example.com")
	b := h.login(t, "claude", "bob@example.com")
	h.assign(t, "claude-a", domain.HarnessClaudeCode, a)
	if err := h.svc.SetPrimaryWithOptions(h.ctx, b, true); err != nil {
		t.Fatal(err)
	}
	h.route(t, "claude-a", b)
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := primary(state, "claude"); got != b {
		t.Fatalf("primary=%q, want %q", got, b)
	}
}
