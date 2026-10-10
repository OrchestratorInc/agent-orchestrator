package provideraccounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type busyProxy struct {
	*fakeProxy
	refuse   bool
	attempts int
}

func (p *busyProxy) ApplyRoutes(ctx context.Context, routes ports.ProviderRouteSnapshot) error {
	p.attempts++
	if p.refuse {
		return ports.ErrProviderAccountBusy
	}
	return p.fakeProxy.ApplyRoutes(ctx, routes)
}
func TestProviderHelperBusyRefusalIsNotQueuedForLater(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "alice-session", domain.HarnessCodex, alice)
	h.assign(t, "bob-session", domain.HarnessCodex, bob)
	stateBefore, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshotBefore := clone(h.proxy.snapshot)
	guardedBefore := len(h.guard.acquired)
	wrapped := &busyProxy{fakeProxy: h.proxy, refuse: true}
	h.svc.proxy = wrapped
	err = h.svc.Remove(h.ctx, bob, "", false)
	if !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("remove error=%v", err)
	}
	stateAfter, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stateAfter, stateBefore) {
		t.Fatal("helper refusal changed AO catalogue")
	}
	if pending != nil {
		t.Fatal("helper refusal queued an account deletion")
	}
	if !reflect.DeepEqual(h.proxy.snapshot, snapshotBefore) {
		t.Fatal("helper refusal changed effective routes")
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("busy account credential was deleted")
	}
	if len(h.guard.acquired) != guardedBefore+1 {
		t.Fatalf("idle admission count=%d", len(h.guard.acquired))
	}
	if h.guard.released != len(h.guard.acquired) {
		t.Fatal("helper refusal left native input paused")
	}
	wrapped.refuse = false
	attemptsBefore := wrapped.attempts
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	if wrapped.attempts != attemptsBefore {
		t.Fatal("idle recovery retried a user action that was refused")
	}
	h.route(t, "bob-session", bob)
	stateAfter, err = h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := account(stateAfter, bob); !exists {
		t.Fatal("busy removal happened when session became idle")
	}
	if err := h.svc.Remove(h.ctx, bob, "", false); err != nil {
		t.Fatal(err)
	}
	h.route(t, "bob-session", alice)
	h.route(t, "alice-session", alice)
	if !reflect.DeepEqual(h.proxy.deleted, []string{"bob@example.test.json"}) {
		t.Fatalf("deleted=%v", h.proxy.deleted)
	}
}
func TestProviderBusyCleanupFailureRemainsExplicitlyRecoverable(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "claude", "alice@example.test")
	bob := h.login(t, "claude", "bob@example.test")
	h.assign(t, "s", domain.HarnessClaudeCode, bob)
	before, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &busyProxy{fakeProxy: h.proxy, refuse: true}
	h.svc.proxy = wrapped
	h.store.fail = "finish"
	err = h.svc.Remove(h.ctx, bob, "", true)
	if !errors.Is(err, ports.ErrProviderAccountRecovery) || errors.Is(err, ports.ErrProviderAccountBusy) || !errors.Is(err, errTestFailure) {
		t.Fatalf("joined error=%v", err)
	}
	h.store.fail = ""
	effective, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(effective, before) || pending == nil {
		t.Fatalf("effective=%+v pending=%+v", effective, pending)
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("busy cleanup failure deleted credentials")
	}
	id, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, alice)
	if id != "" || managed || !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("resolve=%q %v %v", id, managed, err)
	}
	if err := h.svc.Recover(h.ctx); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("recover=%v", err)
	}
	pendingRequired, err := h.svc.RecoveryRequired(h.ctx)
	if err != nil || pendingRequired {
		t.Fatalf("pending=%v err=%v", pendingRequired, err)
	}
	h.route(t, "s", bob)
	wrapped.refuse = false
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s", bob)
	if len(h.proxy.deleted) != 0 {
		t.Fatal("cleared intent was retried on later recovery")
	}
}
func TestProviderPrimaryChangeWhileOldPrimaryBusyDoesNotMoveIt(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	clara := h.login(t, "claude", "clara@example.test")
	h.assign(t, "active-old", domain.HarnessCodex, alice)
	h.assign(t, "other-provider", domain.HarnessClaudeCode, clara)
	h.guard.busy["active-old"] = true
	acquisitionsBefore := len(h.guard.acquired)
	if err := h.svc.SetPrimary(h.ctx, bob); err != nil {
		t.Fatal(err)
	}
	if len(h.guard.acquired) != acquisitionsBefore {
		t.Fatal("primary-only preference tried to mutate an active session")
	}
	h.route(t, "active-old", alice)
	h.route(t, "other-provider", clara)
	current, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || !managed || current != bob {
		t.Fatalf("new Codex default=%q managed=%v err=%v", current, managed, err)
	}
	other, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, "")
	if err != nil || !managed || other != clara {
		t.Fatalf("new Claude default=%q managed=%v err=%v", other, managed, err)
	}
	explicit, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, alice)
	if err != nil || !managed || explicit != alice {
		t.Fatalf("explicit choice=%q managed=%v err=%v", explicit, managed, err)
	}
	h.assign(t, "new-default", domain.HarnessCodex, current)
	h.route(t, "new-default", bob)
	h.route(t, "active-old", alice)
	if len(h.proxy.deleted) != 0 {
		t.Fatal("primary-only preference removed a saved login")
	}
}
func TestProviderSecondaryRemovalOnlyTouchesItsOwnProviderAndSessions(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	clara := h.login(t, "claude", "clara@example.test")
	dan := h.login(t, "claude", "dan@example.test")
	h.assign(t, "alice", domain.HarnessCodex, alice)
	h.assign(t, "bob-1", domain.HarnessCodex, bob)
	h.assign(t, "bob-2", domain.HarnessCodex, bob)
	h.assign(t, "clara", domain.HarnessClaudeCode, clara)
	h.assign(t, "dan", domain.HarnessClaudeCode, dan)
	h.guard.busy["alice"] = true
	h.guard.busy["clara"] = true
	h.guard.busy["dan"] = true
	if err := h.svc.Remove(h.ctx, bob, "", false); err != nil {
		t.Fatal(err)
	}
	h.route(t, "alice", alice)
	h.route(t, "bob-1", alice)
	h.route(t, "bob-2", alice)
	h.route(t, "clara", clara)
	h.route(t, "dan", dan)
	if !reflect.DeepEqual(h.guard.acquired[len(h.guard.acquired)-1], []domain.SessionID{"bob-1", "bob-2"}) {
		t.Fatalf("affected=%v", h.guard.acquired)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Accounts) != 3 || len(state.Routes) != 5 {
		t.Fatalf("remaining accounts/routes=%d/%d", len(state.Accounts), len(state.Routes))
	}
	if _, exists := account(state, bob); exists {
		t.Fatal("removed secondary is still in catalogue")
	}
	if id, _ := primary(state, "codex"); id != alice {
		t.Fatalf("Codex primary=%s", id)
	}
	if id, _ := primary(state, "claude"); id != clara {
		t.Fatalf("Claude primary=%s", id)
	}
	if !reflect.DeepEqual(h.proxy.deleted, []string{"bob@example.test.json"}) {
		t.Fatalf("deleted=%v", h.proxy.deleted)
	}
	if h.guard.released != len(h.guard.acquired) {
		t.Fatal("batch left a paused session")
	}
}
func TestProviderAllAccountsSignedOutRecoversOnlyWaitingSessionsOfLoginProvider(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	clara := h.login(t, "claude", "clara@example.test")
	h.assign(t, "codex-waiting", domain.HarnessCodex, alice)
	h.assign(t, "claude-waiting", domain.HarnessClaudeCode, clara)
	codexBefore, err := h.svc.LaunchAccountEnv(h.ctx, "codex-waiting")
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore, err := h.svc.LaunchAccountEnv(h.ctx, "claude-waiting")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Remove(h.ctx, alice, "", true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Remove(h.ctx, clara, "", true); err != nil {
		t.Fatal(err)
	}
	h.route(t, "codex-waiting", "")
	h.route(t, "claude-waiting", "")
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		id, managed, err := h.svc.ResolveAccount(h.ctx, harness, "")
		if id != "" || !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
			t.Fatalf("%s default=%q %v %v", harness, id, managed, err)
		}
	}
	signedOut, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(signedOut.Accounts) != 2 {
		t.Fatal("sign-out deleted catalogue identities")
	}
	for _, a := range signedOut.Accounts {
		if a.CredentialRef != "" || a.AuthID != "" {
			t.Fatalf("signed-out account retains credentials: %+v", a)
		}
	}
	bob := h.login(t, "codex", "bob@example.test")
	h.route(t, "codex-waiting", bob)
	h.route(t, "claude-waiting", "")
	afterCodex, err := h.svc.LaunchAccountEnv(h.ctx, "codex-waiting")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(codexBefore, afterCodex) {
		t.Fatal("Codex relogin changed the live process endpoint or ticket")
	}
	afterClaude, err := h.svc.LaunchAccountEnv(h.ctx, "claude-waiting")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(claudeBefore, afterClaude) {
		t.Fatal("other-provider recovery changed Claude launch environment")
	}
	_, _, err = h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, "")
	if !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("Claude recovery incorrectly enabled: %v", err)
	}
	_, err = recordLogin(h.ctx, h.svc, "claude", "CLARA@example.test", "refreshed-clara.json", "new-clara-auth", clara)
	if err != nil {
		t.Fatal(err)
	}
	h.route(t, "claude-waiting", clara)
	h.route(t, "codex-waiting", bob)
	final, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := primary(final, "codex"); id != bob {
		t.Fatalf("Codex primary=%s", id)
	}
	if id, _ := primary(final, "claude"); id != clara {
		t.Fatalf("Claude primary=%s", id)
	}
	if len(final.Accounts) != 3 {
		t.Fatal("re-login duplicated retained account identity")
	}
}
func TestProviderRemovalReplacementMustBeSignedInAndMatchProvider(t *testing.T) {
	for _, replacement := range []string{"", "self", "missing", "claude", "signed-out"} {
		t.Run(replacement, func(t *testing.T) {
			h := setupAccounts(t)
			alice := h.login(t, "codex", "alice@example.test")
			bob := h.login(t, "codex", "bob@example.test")
			clara := h.login(t, "claude", "clara@example.test")
			dan := h.login(t, "codex", "dan@example.test")
			h.assign(t, "old-primary", domain.HarnessCodex, alice)
			if err := h.svc.Remove(h.ctx, dan, "", true); err != nil {
				t.Fatal(err)
			}
			before, err := h.svc.State(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			snapshotBefore := clone(h.proxy.snapshot)
			deletionBefore := append([]string(nil), h.proxy.deleted...)
			target := map[string]string{"": "", "self": alice, "missing": "not-found", "claude": clara, "signed-out": dan}[replacement]
			err = h.svc.Remove(h.ctx, alice, target, false)
			if err == nil {
				t.Fatal("invalid replacement allowed primary deletion")
			}
			switch replacement {
			case "", "self":
				if !errors.Is(err, ports.ErrProviderPrimaryRequired) {
					t.Fatalf("error=%v", err)
				}
			case "missing":
				if !errors.Is(err, ports.ErrProviderAccountUnknown) {
					t.Fatalf("error=%v", err)
				}
			case "claude":
				if !errors.Is(err, ports.ErrProviderAccountIncompatible) {
					t.Fatalf("error=%v", err)
				}
			case "signed-out":
				if !errors.Is(err, ports.ErrProviderLoginRequired) {
					t.Fatalf("error=%v", err)
				}
			}
			after, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if pending != nil || !reflect.DeepEqual(after, before) {
				t.Fatal("invalid replacement wrote account facts or recovery intent")
			}
			if !reflect.DeepEqual(h.proxy.snapshot, snapshotBefore) {
				t.Fatal("invalid replacement published routes")
			}
			if !reflect.DeepEqual(h.proxy.deleted, deletionBefore) {
				t.Fatal("invalid replacement deleted a credential")
			}
			h.route(t, "old-primary", alice)
			if err := h.svc.Remove(h.ctx, alice, bob, false); err != nil {
				t.Fatal(err)
			}
			h.route(t, "old-primary", bob)
			final, err := h.svc.State(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if id, _ := primary(final, "codex"); id != bob {
				t.Fatalf("replacement primary=%s", id)
			}
		})
	}
}
func TestProviderSessionTicketRemainsStableAcrossPrimarySwitchAndAccountRemoval(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "persistent", domain.HarnessCodex, alice)
	h.assign(t, "other", domain.HarnessCodex, bob)
	initial, err := h.svc.LaunchAccountEnv(h.ctx, "persistent")
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.svc.LaunchAccountEnv(h.ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	ticket := initial["AO_PROXY_TICKET"]
	if len(ticket) != 64 || ticket == other["AO_PROXY_TICKET"] {
		t.Fatal("tickets are missing or shared between sessions")
	}
	digest := sha256.Sum256([]byte(ticket))
	route, managed, err := h.svc.SessionAccount(h.ctx, "persistent")
	if err != nil || !managed || route.TicketHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("route=%+v managed=%v err=%v", route, managed, err)
	}
	if strings.Contains(ticket, "alice") || strings.Contains(ticket, "bob") {
		t.Fatal("ticket contains account identity")
	}
	if err := h.svc.SetPrimary(h.ctx, bob); err != nil {
		t.Fatal(err)
	}
	h.route(t, "persistent", alice)
	if err := h.svc.Switch(h.ctx, "persistent", bob); err != nil {
		t.Fatal(err)
	}
	h.route(t, "persistent", bob)
	switched, err := h.svc.LaunchAccountEnv(h.ctx, "persistent")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, switched) {
		t.Fatal("manual account switch changed stable session environment")
	}
	if err := h.svc.Remove(h.ctx, bob, alice, true); err != nil {
		t.Fatal(err)
	}
	h.route(t, "persistent", alice)
	afterRemoval, err := h.svc.LaunchAccountEnv(h.ctx, "persistent")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, afterRemoval) {
		t.Fatal("primary removal requires changing native process credentials")
	}
	restored := New(h.store, h.proxy, h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { return "unused" })
	afterRestart, err := restored.LaunchAccountEnv(h.ctx, "persistent")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, afterRestart) {
		t.Fatal("daemon replacement changed stable ticket")
	}
	pending, err := restored.RecoveryRequired(h.ctx)
	if err != nil || pending {
		t.Fatalf("unexpected pending operation=%v err=%v", pending, err)
	}
}
func TestProviderDeletedSeedRevokesItsTicketWithoutChangingOtherRoutes(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	h.assign(t, "failed-spawn", domain.HarnessCodex, alice)
	h.assign(t, "kept", domain.HarnessCodex, alice)
	envBefore, err := h.svc.LaunchAccountEnv(h.ctx, "kept")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ForgetAccount(h.ctx, "failed-spawn"); err != nil {
		t.Fatal(err)
	}
	_, managed, err := h.svc.SessionAccount(h.ctx, "failed-spawn")
	if err != nil || managed {
		t.Fatalf("revoked route managed=%v err=%v", managed, err)
	}
	for _, route := range h.proxy.snapshot.Routes {
		if route.SessionID == "failed-spawn" {
			t.Fatal("helper still accepts deleted seed ticket")
		}
	}
	h.route(t, "kept", alice)
	envAfter, err := h.svc.LaunchAccountEnv(h.ctx, "kept")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envBefore, envAfter) {
		t.Fatal("seed cleanup changed unrelated native environment")
	}
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.Revision
	if err := h.svc.ForgetAccount(h.ctx, "failed-spawn"); err != nil {
		t.Fatal(err)
	}
	state, err = h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != revision {
		t.Fatal("repeated seed cleanup published an unnecessary revision")
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("seed cleanup deleted its shared account credential")
	}
}
func TestProviderBusyDeletedSeedCannotRevokeAnAcceptedRequest(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	h.assign(t, "s", domain.HarnessCodex, alice)
	h.guard.busy["s"] = true
	before := clone(h.proxy.snapshot)
	if err := h.svc.ForgetAccount(h.ctx, "s"); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("cleanup=%v", err)
	}
	h.route(t, "s", alice)
	if !reflect.DeepEqual(h.proxy.snapshot, before) {
		t.Fatal("active cleanup changed helper routes")
	}
	pending, err := h.svc.RecoveryRequired(h.ctx)
	if err != nil || pending {
		t.Fatalf("queued cleanup=%v err=%v", pending, err)
	}
	h.guard.busy["s"] = false
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s", alice)
	if err := h.svc.ForgetAccount(h.ctx, "s"); err != nil {
		t.Fatal(err)
	}
	_, managed, err := h.svc.SessionAccount(h.ctx, "s")
	if err != nil || managed {
		t.Fatalf("retry managed=%v err=%v", managed, err)
	}
}
func TestProviderIdempotentLoginRecoveryKeepsExistingAccountAndPrimary(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "existing", domain.HarnessCodex, alice)
	if err := h.svc.SetPrimary(h.ctx, bob); err != nil {
		t.Fatal(err)
	}
	id, err := recordLogin(h.ctx, h.svc, "codex", "ALICE@example.test", "alice@example.test.json", "alice@example.test-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != alice {
		t.Fatalf("recovered account=%q want=%q", id, alice)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Accounts) != 2 {
		t.Fatal("retried verification duplicated account")
	}
	if current, _ := primary(state, "codex"); current != bob {
		t.Fatalf("retried login changed primary=%s", current)
	}
	h.route(t, "existing", alice)
	if len(h.proxy.deleted) != 0 {
		t.Fatal("idempotent completion deleted a live credential")
	}
	if _, err := recordLogin(h.ctx, h.svc, "codex", "alice@example.test", "different.json", "other-auth", ""); err == nil {
		t.Fatal("unverified duplicate identity overwrote existing credentials")
	}
	after, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, state) {
		t.Fatal("rejected duplicate changed catalogue")
	}
}
func TestProviderCancelledMutationDoesNotAdmitRecoveryIntent(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "s", domain.HarnessCodex, alice)
	before := clone(h.proxy.snapshot)
	h.svc.gate <- struct{}{}
	ctx, cancel := context.WithCancel(h.ctx)
	cancel()
	err := h.svc.Switch(ctx, "s", bob)
	<-h.svc.gate
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	h.route(t, "s", alice)
	if !reflect.DeepEqual(h.proxy.snapshot, before) {
		t.Fatal("cancelled admission changed helper")
	}
	pending, err := h.svc.RecoveryRequired(h.ctx)
	if err != nil || pending {
		t.Fatalf("cancelled intent=%v err=%v", pending, err)
	}
	if h.guard.released != len(h.guard.acquired) {
		t.Fatal("cancelled admission retained native guard")
	}
	if err := h.svc.Switch(h.ctx, "s", bob); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s", bob)
}
