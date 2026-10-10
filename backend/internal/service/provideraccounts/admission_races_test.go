package provideraccounts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type stoppedAccountProxy struct {
	*fakeProxy
	started     chan ports.ProviderRouteSnapshot
	resume      chan struct{}
	acceptFirst bool
}

func (p *stoppedAccountProxy) ApplyRoutes(ctx context.Context, next ports.ProviderRouteSnapshot) error {
	if p.acceptFirst {
		if err := p.fakeProxy.ApplyRoutes(ctx, next); err != nil {
			return err
		}
	}
	select {
	case p.started <- clone(next):
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-p.resume:
		if p.acceptFirst {
			return nil
		}
		return p.fakeProxy.ApplyRoutes(ctx, next)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAccountAdmissionCancellationBeforeAndAfterHelperAcceptanceKeepsRecoverableIntent(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote-accepted-%t", accepted), func(t *testing.T) {
			h := setupAccounts(t)
			alice := h.login(t, "codex", "alice@example.test")
			bob := h.login(t, "codex", "bob@example.test")
			h.assign(t, "s", domain.HarnessCodex, alice)
			before, err := h.svc.State(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			env, err := h.svc.LaunchAccountEnv(h.ctx, "s")
			if err != nil {
				t.Fatal(err)
			}
			peer := &stoppedAccountProxy{fakeProxy: h.proxy, started: make(chan ports.ProviderRouteSnapshot, 1), resume: make(chan struct{}), acceptFirst: accepted}
			h.svc.proxy = peer
			ctx, cancel := context.WithCancel(h.ctx)
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- h.svc.Switch(ctx, "s", bob) }()
			select {
			case snapshot := <-peer.started:
				if snapshot.Revision != before.Revision+1 || snapshot.Routes[0].AuthID != "bob@example.test-auth" {
					t.Fatal("held request did not carry the intended complete mapping")
				}
			case <-time.After(time.Second):
				t.Fatal("account mutation did not reach helper acknowledgement boundary")
			}
			if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, bob); !errors.Is(err, ports.ErrProviderAccountRecovery) {
				t.Fatal("new session ignored admitted but unacknowledged routing")
			}
			current, managed, err := h.svc.SessionAccount(h.ctx, "s")
			if err != nil || !managed || current.AccountID != alice {
				t.Fatal("unacknowledged account switch changed durable assignment")
			}
			stillEnv, err := h.svc.LaunchAccountEnv(h.ctx, "s")
			if err != nil || !reflect.DeepEqual(env, stillEnv) {
				t.Fatal("unacknowledged account switch altered native environment")
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation was hidden: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled acknowledgement wait did not release mutation admission")
			}
			if h.guard.released != len(h.guard.acquired) {
				t.Fatal("cancelled helper wait retained native input fence")
			}
			effective, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending == nil || !reflect.DeepEqual(effective, before) {
				t.Fatal("cancellation rolled back the durable intent or committed without acknowledgement")
			}
			h.svc.proxy = h.proxy
			restarted := New(h.store, h.proxy, h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { return "unexpected" })
			if err := restarted.Recover(h.ctx); err != nil {
				t.Fatal(err)
			}
			h.svc = restarted
			h.route(t, "s", bob)
			final, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending != nil || final.Revision != before.Revision+1 {
				t.Fatal("cancelled operation recovery was not exactly one admitted revision")
			}
			finalEnv, err := h.svc.LaunchAccountEnv(h.ctx, "s")
			if err != nil || !reflect.DeepEqual(env, finalEnv) {
				t.Fatal("cancellation recovery changed native session ticket")
			}
		})
	}
}

func TestAccountAdmissionWaitingCallerCanCancelWithoutWritingItsOwnOperation(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "s", domain.HarnessCodex, alice)
	peer := &stoppedAccountProxy{fakeProxy: h.proxy, started: make(chan ports.ProviderRouteSnapshot, 1), resume: make(chan struct{})}
	h.svc.proxy = peer
	first := make(chan error, 1)
	go func() { first <- h.svc.Switch(h.ctx, "s", bob) }()
	select {
	case <-peer.started:
	case <-time.After(time.Second):
		t.Fatal("first operation did not acquire mutation admission")
	}
	ctx, cancel := context.WithCancel(h.ctx)
	second := make(chan error, 1)
	go func() { second <- h.svc.Remove(ctx, alice, bob, false) }()
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting caller cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiting operation remained behind the account gate")
	}
	close(peer.resume)
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first operation did not finish")
	}
	h.svc.proxy = h.proxy
	h.route(t, "s", bob)
	state, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil {
		t.Fatal("cancelled second operation left a journal entry")
	}
	if _, found := account(state, alice); !found || len(state.Accounts) != 2 {
		t.Fatal("cancelled waiting removal ran after the first operation finished")
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("cancelled waiting removal deleted a provider credential")
	}
	if current, _ := primary(state, "codex"); current != alice {
		t.Fatal("cancelled waiting removal changed primary")
	}
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	stateAfter, err := h.svc.State(h.ctx)
	if err != nil || !reflect.DeepEqual(state, stateAfter) {
		t.Fatal("cancelled waiting action was queued for recovery")
	}
}

func TestAccountConcurrentManualChoicesKeepOneTicketAndIndependentProviderDefault(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	clara := h.login(t, "claude", "clara@example.test")
	h.assign(t, "s", domain.HarnessCodex, alice)
	h.assign(t, "other-provider", domain.HarnessClaudeCode, clara)
	envBefore, err := h.svc.LaunchAccountEnv(h.ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for n := 0; n < 20; n++ {
		choice := []string{alice, bob}[n%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- h.svc.Switch(h.ctx, "s", choice)
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil || state.Revision != stateBefore.Revision+20 {
		t.Fatal("concurrent manual choices lost or duplicated an admitted operation")
	}
	if len(state.Routes) != 2 || len(state.Accounts) != 3 {
		t.Fatal("concurrent switches created duplicate session or account entries")
	}
	if current, _ := primary(state, "codex"); current != alice {
		t.Fatal("manual choices changed new-session primary")
	}
	if current, _ := primary(state, "claude"); current != clara {
		t.Fatal("manual choices changed another provider's primary")
	}
	h.route(t, "other-provider", clara)
	final, managed, err := h.svc.SessionAccount(h.ctx, "s")
	if err != nil || !managed || (final.AccountID != alice && final.AccountID != bob) {
		t.Fatal("concurrent choices left an invalid effective assignment")
	}
	if h.proxy.snapshot.Revision != state.Revision {
		t.Fatal("concurrent choices committed beyond the acknowledged helper revision")
	}
	envAfter, err := h.svc.LaunchAccountEnv(h.ctx, "s")
	if err != nil || !reflect.DeepEqual(envBefore, envAfter) {
		t.Fatal("concurrent choices replaced a native ticket")
	}
	if h.guard.released != len(h.guard.acquired) || len(h.proxy.deleted) != 0 {
		t.Fatal("concurrent choices retained an input fence or deleted a credential")
	}
}

func TestAccountConcurrentSameIdentityLoginKeepsOneCatalogueEntryAndPrimary(t *testing.T) {
	h := setupAccounts(t)
	var idSequence atomic.Int64
	h.svc.newID = func() string { return fmt.Sprintf("concurrent-account-%d", idSequence.Add(1)) }
	var wg sync.WaitGroup
	type loginResult struct {
		id  string
		err error
	}
	results := make(chan loginResult, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := recordLogin(h.ctx, h.svc, "codex", "same@example.test", "same.json", "same-auth", "")
			results <- loginResult{id: id, err: err}
		}()
	}
	wg.Wait()
	close(results)
	canonical := ""
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if canonical == "" {
			canonical = result.id
		}
		if result.id != canonical {
			t.Fatal("retried verified login returned multiple catalogue identities")
		}
	}
	state, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil || len(state.Accounts) != 1 || len(state.Primaries) != 1 {
		t.Fatal("concurrent verification duplicated catalogue or adoption facts")
	}
	if state.Accounts[0].ID != canonical || state.Accounts[0].AuthID != "same-auth" || state.Accounts[0].CredentialRef != "same.json" {
		t.Fatal("concurrent verification replaced the verified credential identity")
	}
	if current, _ := primary(state, "codex"); current != canonical {
		t.Fatal("concurrent first login did not select its one canonical primary")
	}
	if len(h.proxy.deleted) != 0 || len(h.guard.acquired) != 0 {
		t.Fatal("idempotent login retries deleted credentials or changed native sessions")
	}
	choice, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || !managed || choice != canonical {
		t.Fatal("new sessions cannot resolve the canonical concurrent login")
	}
}
