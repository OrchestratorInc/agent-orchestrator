package provideraccounts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type loginProxyFake struct {
	mu                               sync.Mutex
	status                           string
	verified                         ports.VerifiedProviderLogin
	fail                             string
	starts, polls, verifies, cancels []string
}

func (p *loginProxyFake) StartAccountLogin(_ context.Context, provider, id string) (ports.ProviderLogin, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, provider+":"+id)
	if p.fail == "start" {
		return ports.ProviderLogin{}, errTestFailure
	}
	return ports.ProviderLogin{ID: id, Provider: provider, State: "private-state", URL: "https://example.test/login", Status: "waiting"}, nil
}
func (p *loginProxyFake) AccountLoginStatus(_ context.Context, l ports.ProviderLogin) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polls = append(p.polls, l.ID)
	if p.fail == "status" {
		return "", errTestFailure
	}
	return p.status, nil
}
func (p *loginProxyFake) CancelAccountLogin(_ context.Context, l ports.ProviderLogin) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancels = append(p.cancels, l.ID)
	if p.fail == "cancel" {
		return errTestFailure
	}
	return nil
}
func (p *loginProxyFake) VerifiedAccountLogin(_ context.Context, id string) (ports.VerifiedProviderLogin, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.verifies = append(p.verifies, id)
	// The helper has a credential to report only once the provider granted one.
	if p.fail == "verify" || p.status != "complete" {
		return ports.VerifiedProviderLogin{}, errTestFailure
	}
	return p.verified, nil
}
func loginCoordinatorHarness(t *testing.T) (accountHarness, *LoginCoordinator, *loginProxyFake) {
	t.Helper()
	h := setupAccounts(t)
	p := &loginProxyFake{status: "waiting", verified: ports.VerifiedProviderLogin{Provider: "codex", Email: "alice@example.test", CredentialRef: "alice.json", AuthID: "auth-alice"}}
	n := 0
	l := NewLoginCoordinator(h.svc, p, func() string { n++; return fmt.Sprintf("login-%d", n) })
	return h, l, p
}
func TestProviderLoginAddsVerifiedAccountExactlyOnce(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	login, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if login.ID != "login-1" || login.Status != "waiting" || login.URL == "" {
		t.Fatalf("login=%+v", login)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 0 {
		t.Fatalf("premature account=%+v err=%v", state, err)
	}
	waiting, err := l.Status(h.ctx, login.ID)
	if err != nil || waiting.Status != "waiting" || len(p.verifies) != 0 {
		t.Fatalf("waiting=%+v verifies=%v err=%v", waiting, p.verifies, err)
	}
	p.status = "complete"
	complete, err := l.Status(h.ctx, login.ID)
	if err != nil {
		t.Fatal(err)
	}
	if complete.Status != "complete" || complete.AccountID == "" {
		t.Fatalf("complete=%+v", complete)
	}
	state, err = h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || state.Accounts[0].ID != complete.AccountID || state.Accounts[0].Email != p.verified.Email {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if primaryID, managed := primary(state, "codex"); !managed || primaryID != complete.AccountID {
		t.Fatalf("primary=%s managed=%v", primaryID, managed)
	}
	again, err := l.Status(h.ctx, login.ID)
	if err != nil || !reflect.DeepEqual(again, complete) || len(p.verifies) != 1 {
		t.Fatalf("repeat=%+v verifies=%v err=%v", again, p.verifies, err)
	}
	if err = l.Cancel(h.ctx, login.ID); err != nil || len(p.cancels) != 0 {
		t.Fatalf("completed cancellation=%v calls=%v", err, p.cancels)
	}
}
func TestProviderLoginAllowsIndependentProvidersButOneAttemptEach(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	codex, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := l.Start(h.ctx, "codex", ""); err != nil || resumed.ID != codex.ID {
		t.Fatalf("same-provider attempt was not resumed: %+v %v", resumed, err)
	}
	claude, err := l.Start(h.ctx, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if claude.ID == codex.ID || len(p.starts) != 2 {
		t.Fatalf("starts=%v", p.starts)
	}
	if err = l.Cancel(h.ctx, codex.ID); err != nil {
		t.Fatal(err)
	}
	next, err := l.Start(h.ctx, "codex", "")
	if err != nil || next.ID == codex.ID {
		t.Fatalf("new=%+v err=%v", next, err)
	}
	if resumed, err := l.Start(h.ctx, "claude", ""); err != nil || resumed.ID != claude.ID {
		t.Fatalf("codex cancellation changed Claude login: %+v %v", resumed, err)
	}
}

func TestProviderLoginStatusFailureAfterDeadlineBecomesTerminal(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	login, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	p.fail = "status"
	l.now = func() time.Time { return time.Now().Add(7 * time.Minute) }
	result, err := l.Status(h.ctx, login.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("expired login remained active: %+v", result)
	}
	if len(p.cancels) != 1 {
		t.Fatalf("expired login was not cancelled: %v", p.cancels)
	}
}
func TestProviderLoginCancellationIsIdempotentAndKeepsInventory(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	existing := h.login(t, "codex", "existing@example.test")
	login, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = l.Cancel(h.ctx, login.ID); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, err := l.Status(h.ctx, login.ID)
	if err != nil || cancelled.Status != "cancelled" || len(p.cancels) != 1 || len(p.polls) != 0 {
		t.Fatalf("cancelled=%+v cancel=%v polls=%v err=%v", cancelled, p.cancels, p.polls, err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || state.Accounts[0].ID != existing {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err = l.Cancel(h.ctx, "unknown-attempt"); err != nil {
		t.Fatal(err)
	}
}

// The provider can grant a sign-in a moment before its attempt is cancelled or
// runs out of time. Nothing records that credential afterwards, so it must not
// be left in the helper.
func TestProviderLoginEndedAttemptRemovesTheSignInItHadAlreadySaved(t *testing.T) {
	for name, end := range map[string]func(*testing.T, accountHarness, *LoginCoordinator, ports.ProviderLogin){
		"cancelled": func(t *testing.T, h accountHarness, l *LoginCoordinator, login ports.ProviderLogin) {
			if err := l.Cancel(h.ctx, login.ID); err != nil {
				t.Fatal(err)
			}
		},
		"out of time": func(t *testing.T, h accountHarness, l *LoginCoordinator, login ports.ProviderLogin) {
			l.now = func() time.Time { return time.Now().Add(7 * time.Minute) }
			if result, err := l.Status(h.ctx, login.ID); err != nil || result.Status != "failed" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, l, p := loginCoordinatorHarness(t)
			login, err := l.Start(h.ctx, "codex", "")
			if err != nil {
				t.Fatal(err)
			}
			if name == "out of time" {
				// The helper cannot be asked how the attempt went, but has saved the sign-in.
				p.fail = "status"
			}
			p.status = "complete"
			end(t, h, l, login)
			if !reflect.DeepEqual(h.proxy.deleted, []string{"alice.json"}) {
				t.Fatalf("deleted=%v, want the saved sign-in removed", h.proxy.deleted)
			}
			if state, err := h.svc.State(h.ctx); err != nil || len(state.Accounts) != 0 {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
}

func TestProviderLoginEndedAttemptKeepsASignInAnAccountUses(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	login, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	// Another path recorded the very credential this attempt produced.
	if _, err := h.svc.RecordCredential(h.ctx, p.verified, ""); err != nil {
		t.Fatal(err)
	}
	p.status = "complete"
	if err := l.Cancel(h.ctx, login.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatalf("deleted=%v, want the account's sign-in kept", h.proxy.deleted)
	}
}

func TestProviderLoginFailedStatusNeverCreatesAccount(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	login, err := l.Start(h.ctx, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	p.status = "failed"
	failed, err := l.Status(h.ctx, login.ID)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 0 || len(state.Primaries) != 0 || len(p.verifies) != 0 {
		t.Fatalf("state=%+v verifies=%v err=%v", state, p.verifies, err)
	}
	if _, err = l.Start(h.ctx, "claude", ""); err != nil {
		t.Fatalf("retry after failure=%v", err)
	}
}
func TestProviderLoginReloginPreservesSavedEntryAndRecoversWaitingSessions(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			h, l, p := loginCoordinatorHarness(t)
			id := h.login(t, provider, "alice@example.test")
			harness := domain.HarnessCodex
			if provider == "claude" {
				harness = domain.HarnessClaudeCode
			}
			h.assign(t, "existing", harness, id)
			if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
				t.Fatal(err)
			}
			login, err := l.Start(h.ctx, provider, id)
			if err != nil {
				t.Fatal(err)
			}
			p.status = "complete"
			p.verified = ports.VerifiedProviderLogin{Provider: provider, Email: "ALICE@example.test", CredentialRef: "renewed.json", AuthID: "renewed-auth"}
			complete, err := l.Status(h.ctx, login.ID)
			if err != nil || complete.AccountID != id {
				t.Fatalf("complete=%+v err=%v", complete, err)
			}
			h.route(t, "existing", id)
			state, err := h.svc.State(h.ctx)
			if err != nil || len(state.Accounts) != 1 || state.Accounts[0].CredentialRef != "renewed.json" {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
}
func TestProviderLoginRefusesInvalidReloginBeforeOpeningBrowser(t *testing.T) {
	cases := []struct {
		name, provider, kind string
		want                 error
	}{
		{"unsupported", "gemini", "new", nil},
		{"unknown", "codex", "unknown", ports.ErrProviderAccountUnknown},
		{"different-provider", "claude", "signed-out", ports.ErrProviderAccountIncompatible},
		{"already-signed-in", "codex", "signed-in", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, l, p := loginCoordinatorHarness(t)
			id := ""
			switch tc.kind {
			case "unknown":
				id = "unknown"
			case "signed-in", "signed-out":
				id = h.login(t, "codex", "a@example.test")
				if tc.kind == "signed-out" {
					if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err := l.Start(h.ctx, tc.provider, id)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			if len(p.starts) != 0 {
				t.Fatalf("unacceptable login reached provider: %v", p.starts)
			}
		})
	}
}
func TestProviderLoginVerifiedIdentityMustMatchChosenEntry(t *testing.T) {
	cases := []struct{ name, provider, email, ref, auth string }{
		{"wrong-provider", "claude", "alice@example.test", "fresh.json", "fresh-auth"},
		{"wrong-email", "codex", "mallory@example.test", "fresh.json", "fresh-auth"},
		{"empty-email", "codex", "", "fresh.json", "fresh-auth"},
		{"empty-reference", "codex", "alice@example.test", "", "fresh-auth"},
		{"empty-auth-id", "codex", "alice@example.test", "fresh.json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, l, p := loginCoordinatorHarness(t)
			id := h.login(t, "codex", "alice@example.test")
			if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
				t.Fatal(err)
			}
			login, err := l.Start(h.ctx, "codex", id)
			if err != nil {
				t.Fatal(err)
			}
			p.status = "complete"
			p.verified = ports.VerifiedProviderLogin{Provider: tc.provider, Email: tc.email, CredentialRef: tc.ref, AuthID: tc.auth}
			if _, err = l.Status(h.ctx, login.ID); err == nil {
				t.Fatal("unverified identity admitted")
			}
			state, err := h.svc.State(h.ctx)
			if err != nil || len(state.Accounts) != 1 || state.Accounts[0].CredentialRef != "" || state.Accounts[0].AuthID != "" {
				t.Fatalf("saved signed-out entry changed=%+v err=%v", state, err)
			}
			if primaryID, _ := primary(state, "codex"); primaryID != "" {
				t.Fatalf("wrong login became primary=%s", primaryID)
			}
		})
	}
}
func TestProviderLoginProxyFailuresRemainRetryable(t *testing.T) {
	for _, stage := range []string{"start", "status", "verify", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			h, l, p := loginCoordinatorHarness(t)
			if stage == "start" {
				p.fail = stage
				if _, err := l.Start(h.ctx, "codex", ""); !errors.Is(err, errTestFailure) {
					t.Fatalf("start error=%v", err)
				}
				p.fail = ""
			}
			login, err := l.Start(h.ctx, "codex", "")
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "status":
				p.fail = stage
				if _, err = l.Status(h.ctx, login.ID); !errors.Is(err, errTestFailure) {
					t.Fatalf("status error=%v", err)
				}
			case "verify":
				p.status = "complete"
				p.fail = stage
				if _, err = l.Status(h.ctx, login.ID); !errors.Is(err, errTestFailure) {
					t.Fatalf("verify error=%v", err)
				}
			case "cancel":
				p.fail = stage
				if err = l.Cancel(h.ctx, login.ID); !errors.Is(err, errTestFailure) {
					t.Fatalf("cancel error=%v", err)
				}
			}
			p.fail = ""
			p.status = "complete"
			complete, err := l.Status(h.ctx, login.ID)
			if err != nil || complete.Status != "complete" {
				t.Fatalf("retry=%+v err=%v", complete, err)
			}
		})
	}
}
func TestProviderLoginRecordCommitFailureRecoversWithoutDuplicateAccount(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	login, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	p.status = "complete"
	h.store.fail = "commit"
	if _, err = l.Status(h.ctx, login.ID); err == nil {
		t.Fatal("expected durable commit failure")
	}
	if h.store.pending == nil {
		t.Fatal("verified login lost recovery intent")
	}
	h.store.fail = ""
	if err = h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	// After a lost acknowledgement the coordinator must recognize this verified
	// login as its own previously committed account, rather than attempt a second
	// inventory entry with the same email.
	complete, err := l.Status(h.ctx, login.ID)
	if err != nil || complete.Status != "complete" {
		t.Fatalf("coordinator failed after recovery: %+v %v", complete, err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 {
		t.Fatalf("duplicates=%+v err=%v", state, err)
	}
}
func TestProviderLoginRestartRequiresFreshAttempt(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	login, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewLoginCoordinator(h.svc, p, func() string { return "fresh" })
	if _, err = restarted.Status(h.ctx, login.ID); err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("old browser attempt err=%v", err)
	}
	if err = restarted.Cancel(h.ctx, login.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Start(h.ctx, "codex", ""); err != nil {
		t.Fatal(err)
	}
}
