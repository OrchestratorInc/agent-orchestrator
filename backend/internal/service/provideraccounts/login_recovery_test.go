package provideraccounts

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProviderLoginLostRendererStateResumesSameAttempt(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	first, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		resumed, err := l.Start(h.ctx, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resumed, first) {
			t.Fatalf("renderer restart changed login=%+v", resumed)
		}
	}
	if len(p.starts) != 1 || len(p.polls) != 5 || len(p.verifies) != 0 {
		t.Fatalf("upstream operations starts=%v polls=%v verifies=%v", p.starts, p.polls, p.verifies)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 0 {
		t.Fatal("resuming a pending browser created an account")
	}
	p.status = "complete"
	complete, err := l.Status(h.ctx, first.ID)
	if err != nil || complete.Status != "complete" {
		t.Fatalf("resumed completion=%+v %v", complete, err)
	}
	if len(p.verifies) != 1 {
		t.Fatal("resumed attempt verified more than once")
	}
	state, err = h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || state.Accounts[0].ID != complete.AccountID {
		t.Fatal("resumed login did not create exactly one saved identity")
	}
}
func TestProviderLoginConcurrentAddResumesOneBrowser(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	const callers = 12
	results := make(chan ports.ProviderLogin, callers)
	failures := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			login, err := l.Start(context.Background(), "claude", "")
			if err != nil {
				failures <- err
				return
			}
			results <- login
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var first ports.ProviderLogin
	count := 0
	for login := range results {
		count++
		if first.ID == "" {
			first = login
		}
		if login.ID != first.ID || login.URL != first.URL || login.State != first.State {
			t.Fatal("concurrent caller received a different browser attempt")
		}
	}
	if count != callers || len(p.starts) != 1 {
		t.Fatalf("results=%d starts=%v", count, p.starts)
	}
	if len(p.cancels) != 0 || len(p.verifies) != 0 {
		t.Fatal("resumed callers cancelled or verified waiting login")
	}
	if _, pending, err := h.store.LoadProviderAccountState(h.ctx); err != nil || pending != nil {
		t.Fatal("pending browser admitted an account mutation")
	}
}
func TestProviderLoginResumingOtherSavedEntryRequiresExplicitCancel(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	a := h.login(t, "codex", "alice@example.test")
	b := h.login(t, "codex", "bob@example.test")
	if err := h.svc.Remove(h.ctx, b, "", true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Remove(h.ctx, a, "", true); err != nil {
		t.Fatal(err)
	}
	first, err := l.Start(h.ctx, "codex", a)
	if err != nil {
		t.Fatal(err)
	}
	for _, different := range []string{b, ""} {
		if _, err = l.Start(h.ctx, "codex", different); !errors.Is(err, ports.ErrProviderAccountConflict) {
			t.Fatalf("different selected entry error=%v", err)
		}
	}
	if len(p.starts) != 1 || len(p.cancels) != 0 {
		t.Fatal("changing chosen entry silently replaced pending browser")
	}
	resumed, err := l.Start(h.ctx, "codex", a)
	if err != nil || resumed.ID != first.ID {
		t.Fatalf("chosen entry did not resume: %+v %v", resumed, err)
	}
	if err = l.Cancel(h.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := l.Start(h.ctx, "codex", b)
	if err != nil || second.ID == first.ID || second.AccountID != b {
		t.Fatalf("explicit new entry=%+v %v", second, err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 2 {
		t.Fatal("cancelling login removed a saved entry")
	}
	for _, a := range state.Accounts {
		if a.CredentialRef != "" {
			t.Fatal("uncompleted browser restored credentials")
		}
	}
}
func TestProviderLoginExpiryAllowsFreshBrowserWithoutAccountMutation(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			h, l, p := loginCoordinatorHarness(t)
			now := time.Unix(100, 0)
			l.now = func() time.Time { return now }
			first, err := l.Start(h.ctx, provider, "")
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(6 * time.Minute)
			expired, err := l.Status(h.ctx, first.ID)
			if err != nil || expired.Status != "failed" {
				t.Fatalf("expiry=%+v %v", expired, err)
			}
			if !reflect.DeepEqual(p.cancels, []string{first.ID}) {
				t.Fatalf("expired upstream attempts=%v", p.cancels)
			}
			again, err := l.Status(h.ctx, first.ID)
			if err != nil || again.Status != "failed" || len(p.cancels) != 1 {
				t.Fatal("expiry polling repeated provider cancellation")
			}
			state, err := h.svc.State(h.ctx)
			if err != nil || len(state.Accounts) != 0 || len(state.Primaries) != 0 {
				t.Fatal("expired browser adopted account routing")
			}
			next, err := l.Start(h.ctx, provider, "")
			if err != nil || next.ID == first.ID || next.Status != "waiting" {
				t.Fatalf("fresh browser=%+v %v", next, err)
			}
		})
	}
}
func TestProviderLoginExpiryCancellationFailureIsRetryable(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	first, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(7 * time.Minute)
	p.fail = "cancel"
	failed, err := l.Status(h.ctx, first.ID)
	if !errors.Is(err, errTestFailure) || failed.Status != "waiting" {
		t.Fatalf("expiry failure=%+v %v", failed, err)
	}
	if len(p.starts) != 1 || len(p.verifies) != 0 {
		t.Fatal("expiry cleanup failure admitted another attempt")
	}
	p.fail = ""
	next, err := l.Start(h.ctx, "codex", "")
	if err != nil || next.ID == first.ID {
		t.Fatalf("expiry recovery=%+v %v", next, err)
	}
	if len(p.cancels) != 2 || len(p.starts) != 2 {
		t.Fatal("expiry cleanup was not retried before fresh browser")
	}
}
func TestProviderLoginCompletedAfterPanelAbsenceIsRecordedBeforeNextAdd(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	now := time.Unix(100, 0)
	l.now = func() time.Time { return now }
	first, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * time.Minute)
	p.status = "complete"
	next, err := l.Start(h.ctx, "codex", "")
	if err != nil || next.ID == first.ID {
		t.Fatalf("next add=%+v %v", next, err)
	}
	complete, err := l.Status(h.ctx, first.ID)
	if err != nil || complete.Status != "complete" || complete.AccountID == "" {
		t.Fatalf("completed browser was lost=%+v %v", complete, err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || state.Accounts[0].ID != complete.AccountID {
		t.Fatal("late browser did not save its verified account")
	}
	if len(p.cancels) != 0 {
		t.Fatal("completed browser was treated as expired")
	}
}
func TestProviderLoginRejectedIdentityReleasesAttemptAndDeletesOnlyUnusedCredential(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	id := h.login(t, "codex", "alice@example.test")
	h.assign(t, "existing", domain.HarnessCodex, id)
	if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
		t.Fatal(err)
	}
	before, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := l.Start(h.ctx, "codex", id)
	if err != nil {
		t.Fatal(err)
	}
	p.status = "complete"
	p.verified = ports.VerifiedProviderLogin{Provider: "codex", Email: "bob@example.test", CredentialRef: "bob.json", AuthID: "auth-bob"}
	failed, err := l.Status(h.ctx, first.ID)
	if !errors.Is(err, ports.ErrProviderAccountIncompatible) || failed.Status != "failed" {
		t.Fatalf("mismatch=%+v %v", failed, err)
	}
	if !reflect.DeepEqual(h.proxy.deleted, []string{"alice@example.test.json", "bob.json"}) {
		t.Fatalf("unused credential cleanup=%v", h.proxy.deleted)
	}
	after, err := h.svc.State(h.ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected identity changed saved sessions or accounts")
	}
	p.status = "waiting"
	next, err := l.Start(h.ctx, "codex", id)
	if err != nil || next.ID == first.ID {
		t.Fatalf("retry after mismatch=%+v %v", next, err)
	}
	h.route(t, "existing", "")
}
func TestProviderLoginRejectedIdentityCannotDeleteAnExistingAccountsCredential(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "bob-session", domain.HarnessCodex, bob)
	if err := h.svc.Remove(h.ctx, alice, bob, true); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := l.Start(h.ctx, "codex", alice)
	if err != nil {
		t.Fatal(err)
	}
	p.status = "complete"
	p.verified = ports.VerifiedProviderLogin{Provider: "codex", Email: "bob@example.test", CredentialRef: "bob@example.test.json", AuthID: "auth-bob@example.test"}
	if _, err = l.Status(h.ctx, first.ID); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("wrong saved entry=%v", err)
	}
	if !reflect.DeepEqual(h.proxy.deleted, []string{"alice@example.test.json"}) {
		t.Fatalf("rejection deleted an in-use credential=%v", h.proxy.deleted)
	}
	after, err := h.svc.State(h.ctx)
	if err != nil || !reflect.DeepEqual(after, stateBefore) {
		t.Fatal("rejected login changed another account")
	}
	h.route(t, "bob-session", bob)
}
func TestProviderLoginRejectedCredentialCleanupChecksPendingFacts(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "claude", "alice@example.test")
	before, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := clone(before)
	next.Revision++
	next.Accounts = append(next.Accounts, domain.ProviderAccount{ID: "pending", Provider: "claude", Email: "pending@example.test", CredentialRef: "pending.json", AuthID: "auth-pending"})
	if err = h.store.SaveProviderAccountIntent(h.ctx, before.Revision, domain.ProviderAccountIntent{Next: next}); err != nil {
		t.Fatal(err)
	}
	if err = h.svc.discardLogin(h.ctx, ports.VerifiedProviderLogin{CredentialRef: "pending.json"}); err != nil {
		t.Fatal(err)
	}
	if err = h.svc.discardLogin(h.ctx, ports.VerifiedProviderLogin{CredentialRef: "alice@example.test.json"}); err != nil {
		t.Fatal(err)
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("login rejection deleted effective or admitted credentials")
	}
	current, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending == nil || !reflect.DeepEqual(current, before) {
		t.Fatal("cleanup reconciled a different pending operation")
	}
	if current.Accounts[0].ID != alice {
		t.Fatal("effective identity changed")
	}
}
func TestProviderLoginRejectedCleanupFailureDoesNotAdmitWrongIdentity(t *testing.T) {
	h, l, p := loginCoordinatorHarness(t)
	first, err := l.Start(h.ctx, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	p.status = "complete"
	p.verified = ports.VerifiedProviderLogin{Provider: "claude", Email: "wrong@example.test", CredentialRef: "unused.json", AuthID: "unused"}
	h.proxy.fail = "delete"
	failed, err := l.Status(h.ctx, first.ID)
	if !errors.Is(err, ports.ErrProviderAccountIncompatible) || !errors.Is(err, errTestFailure) || failed.Status != "failed" {
		t.Fatalf("rejected cleanup=%+v %v", failed, err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 0 || len(state.Routes) != 0 {
		t.Fatal("failed unused credential cleanup created managed routing")
	}
	h.proxy.fail = ""
	p.status = "waiting"
	retry, err := l.Start(h.ctx, "codex", "")
	if err != nil || retry.ID == first.ID {
		t.Fatalf("rejected browser blocked future login=%+v %v", retry, err)
	}
}
