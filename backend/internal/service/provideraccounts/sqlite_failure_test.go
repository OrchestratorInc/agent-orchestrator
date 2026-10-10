package provideraccounts

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func failAccountSQLBoundary(t *testing.T, h *accountWireHarness, boundary string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(h.data, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	condition := map[string]string{
		"save":   "OLD.pending IS NULL AND NEW.pending IS NOT NULL",
		"commit": "NEW.revision <> OLD.revision",
		"finish": "OLD.pending IS NOT NULL AND NEW.pending IS NULL",
	}[boundary]
	if condition == "" {
		t.Fatal("unknown SQL failure boundary")
	}
	statement := "CREATE TRIGGER test_account_boundary BEFORE UPDATE ON provider_account_state WHEN " + condition + " BEGIN SELECT RAISE(ABORT, 'test account write failure'); END"
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := db.Exec("DROP TRIGGER test_account_boundary"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAccountSQLFailureDoesNotLoseRoutingIntentOrDeleteCredentialPrematurely(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, boundary := range []string{"save", "commit", "finish"} {
			t.Run(provider+"/"+boundary, func(t *testing.T) {
				h := newAccountWireHarness(t)
				alice := h.login(t, provider, "alice@example.test", "")
				bob := h.login(t, provider, "bob@example.test", "")
				env := h.assign(t, "affected", journeyHarness(provider), bob)
				before, err := h.svc.State(h.ctx)
				if err != nil {
					t.Fatal(err)
				}
				disableFailure := failAccountSQLBoundary(t, h, boundary)
				if err := h.svc.Remove(h.ctx, bob, "", true); err == nil {
					t.Fatal("SQL journal failure was reported as a successful sign-out")
				}
				if h.guard.released != len(h.guard.acquired) {
					t.Fatal("SQL failure retained affected session input fence")
				}
				effective, pending, err := h.store.LoadProviderAccountState(h.ctx)
				if err != nil {
					t.Fatal(err)
				}
				h.peer.mu.Lock()
				remote := clone(h.peer.snapshot)
				deleted := append([]string(nil), h.peer.deleted...)
				credentialPresent := h.peer.credentials[provider+"-bob@example.test.json"]
				h.peer.mu.Unlock()
				switch boundary {
				case "save":
					if pending != nil || !reflect.DeepEqual(before, effective) || remote.Revision != before.Revision {
						t.Fatal("journal admission failure changed local or remote account routing")
					}
					if !credentialPresent || len(deleted) != 0 {
						t.Fatal("failed journal admission deleted provider credentials")
					}
				case "commit":
					if pending == nil || !reflect.DeepEqual(before, effective) || remote.Revision != before.Revision+1 {
						t.Fatal("failed local commit lost the acknowledged remote operation")
					}
					if !credentialPresent || len(deleted) != 0 {
						t.Fatal("provider credential was deleted before AO committed reassignment")
					}
				case "finish":
					if pending == nil || effective.Revision != before.Revision+1 || remote.Revision != effective.Revision {
						t.Fatal("failed journal cleanup rolled back committed facts")
					}
					entry, found := account(effective, bob)
					if !found || entry.CredentialRef != "" || entry.AuthID != "" {
						t.Fatal("finished sign-out did not retain an empty catalogue entry")
					}
					if credentialPresent || len(deleted) != 1 {
						t.Fatal("completed credential cleanup was not reflected by the peer")
					}
				}
				disableFailure()
				h.restartDaemon(t)
				if err := h.svc.Recover(h.ctx); err != nil {
					t.Fatal(err)
				}
				if boundary == "save" {
					h.assertAssignment(t, "affected", bob, env)
					if err := h.svc.Remove(h.ctx, bob, "", true); err != nil {
						t.Fatal(err)
					}
				}
				h.assertAssignment(t, "affected", alice, env)
				final, pending, err := h.store.LoadProviderAccountState(h.ctx)
				if err != nil || pending != nil || final.Revision != before.Revision+1 {
					t.Fatal("SQL failure recovery did not finish the one admitted operation")
				}
				entry, found := account(final, bob)
				if !found || entry.CredentialRef != "" || entry.AuthID != "" {
					t.Fatal("recovery removed or re-authenticated a signed-out catalogue row")
				}
				h.peer.mu.Lock()
				defer h.peer.mu.Unlock()
				if len(h.peer.deleted) != 1 || h.peer.deleted[0] != provider+"-bob@example.test.json" {
					t.Fatal("recovery repeated credential deletion or removed another account")
				}
			})
		}
	}
}

func TestAccountSQLRecoveryRespectsNewlyBusyNativeSessionBeforeReplayingAcceptedIntent(t *testing.T) {
	h := newAccountWireHarness(t)
	alice := h.login(t, "codex", "alice@example.test", "")
	bob := h.login(t, "codex", "bob@example.test", "")
	env := h.assign(t, "s", domain.HarnessCodex, alice)
	disableFailure := failAccountSQLBoundary(t, h, "commit")
	if err := h.svc.Switch(h.ctx, "s", bob); err == nil {
		t.Fatal("commit interruption was not reported")
	}
	disableFailure()
	h.restartDaemon(t)
	h.guard.busy["s"] = true
	h.peer.mu.Lock()
	requestsBefore := len(h.peer.requests)
	h.peer.mu.Unlock()
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatalf("request-boundary recovery refused an admitted rebind: %v", err)
	}
	h.peer.mu.Lock()
	requestsAfter := len(h.peer.requests)
	h.peer.mu.Unlock()
	if requestsAfter == requestsBefore {
		t.Fatal("request-boundary recovery did not replay the admitted rebind")
	}
	route, managed, err := h.svc.SessionAccount(h.ctx, "s")
	if err != nil || !managed || route.AccountID != bob {
		t.Fatal("busy recovery did not commit local session reassignment")
	}
	required, err := h.svc.RecoveryRequired(h.ctx)
	if err != nil || required {
		t.Fatal("busy recovery left an admitted mutation unresolved")
	}
	h.assertAssignment(t, "s", bob, env)
}

func TestAccountSQLJournalRejectsSecondDaemonMutationUntilFirstOneIsRecovered(t *testing.T) {
	h := newAccountWireHarness(t)
	alice := h.login(t, "codex", "alice@example.test", "")
	bob := h.login(t, "codex", "bob@example.test", "")
	env := h.assign(t, "s", domain.HarnessCodex, alice)
	h.peer.mu.Lock()
	h.peer.applyMode = "reject"
	h.peer.mu.Unlock()
	if err := h.svc.Switch(h.ctx, "s", bob); err == nil {
		t.Fatal("first daemon should retain its interrupted switch")
	}
	secondStore, err := sqlite.OpenPreMigrated(h.data)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	key, err := h.client.TicketKey()
	if err != nil {
		t.Fatal(err)
	}
	second := New(secondStore, h.client, h.guard, key, h.client.Endpoint(), func() string { return "second-daemon-entry" })
	if err := second.SetPrimary(h.ctx, bob); err == nil {
		t.Fatal("second daemon bypassed unresolved helper rejection")
	}
	state, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending == nil || pending.Next.Revision != state.Revision+1 {
		t.Fatal("second daemon overwrote or dropped the first durable intent")
	}
	if current, _ := primary(state, "codex"); current != alice {
		t.Fatal("second daemon changed the primary before recovery")
	}
	h.peer.mu.Lock()
	h.peer.applyMode = ""
	h.peer.mu.Unlock()
	if err := second.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.assertAssignment(t, "s", bob, env)
	if err := second.SetPrimary(h.ctx, bob); err != nil {
		t.Fatal(err)
	}
	choice, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || !managed || choice != bob {
		t.Fatalf("other daemon did not see committed primary: %s %v %v", choice, managed, err)
	}
	if err := h.svc.AssignAccount(h.ctx, "first-daemon-new", domain.HarnessCodex, choice); err != nil {
		t.Fatal(err)
	}
	state, err = second.State(h.ctx)
	if err != nil || len(state.Routes) != 2 {
		t.Fatal("first daemon assignment was not visible to the replacement")
	}
	for _, account := range state.Accounts {
		if account.ID != alice && account.ID != bob {
			t.Fatalf("recovery manufactured catalogue entry %s", fmt.Sprint(account.ID))
		}
	}
}

func TestAccountSQLBusyRefusalWithFailedAbortRequiresRecovery(t *testing.T) {
	for _, remainsBusy := range []bool{false, true} {
		t.Run(fmt.Sprintf("helper-still-busy=%v", remainsBusy), func(t *testing.T) {
			h := newAccountWireHarness(t)
			alice := h.login(t, "codex", "alice@example.test", "")
			bob := h.login(t, "codex", "bob@example.test", "")
			env := h.assign(t, "affected", domain.HarnessCodex, bob)
			before, err := h.svc.State(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			disableFailure := failAccountSQLBoundary(t, h, "finish")
			h.peer.mu.Lock()
			h.peer.busy = true
			h.peer.mu.Unlock()
			err = h.svc.Remove(h.ctx, bob, "", true)
			if !errors.Is(err, ports.ErrProviderAccountRecovery) || errors.Is(err, ports.ErrProviderAccountBusy) {
				t.Fatalf("unresolved durable intent must report recovery, not a plain busy refusal: %v", err)
			}
			state, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending == nil || !reflect.DeepEqual(before, state) {
				t.Fatal("failed abort lost intent or changed committed facts")
			}
			if h.guard.released != len(h.guard.acquired) {
				t.Fatal("failed abort retained input fence")
			}
			h.peer.mu.Lock()
			unchanged := reflect.DeepEqual(h.peer.snapshot, snapshot(before))
			credentialPresent := h.peer.credentials["codex-bob@example.test.json"]
			deleted := len(h.peer.deleted)
			h.peer.mu.Unlock()
			if !unchanged || !credentialPresent || deleted != 0 {
				t.Fatal("busy refusal changed helper state")
			}
			if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); !errors.Is(err, ports.ErrProviderAccountRecovery) {
				t.Fatalf("new assignment must wait for recovery: %v", err)
			}
			disableFailure()
			h.peer.mu.Lock()
			h.peer.busy = remainsBusy
			h.peer.mu.Unlock()
			h.restartDaemon(t)
			err = h.svc.Recover(h.ctx)
			if remainsBusy {
				if !errors.Is(err, ports.ErrProviderAccountBusy) {
					t.Fatalf("recovery refusal: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			state, pending, err = h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending != nil {
				t.Fatal("recovery did not resolve pending operation")
			}
			if remainsBusy {
				h.peer.mu.Lock()
				h.peer.busy = false
				h.peer.mu.Unlock()
				if err := h.svc.Recover(h.ctx); err != nil {
					t.Fatal(err)
				}
				h.assertAssignment(t, "affected", bob, env)
				if !reflect.DeepEqual(before, state) {
					t.Fatal("durably aborted operation changed facts")
				}
			} else {
				h.assertAssignment(t, "affected", alice, env)
				entry, found := account(state, bob)
				if !found || entry.CredentialRef != "" || state.Revision != before.Revision+1 {
					t.Fatal("recovery did not finish the one admitted sign-out")
				}
			}
			h.peer.mu.Lock()
			defer h.peer.mu.Unlock()
			if remainsBusy && len(h.peer.deleted) != 0 || !remainsBusy && len(h.peer.deleted) != 1 {
				t.Fatal("credential cleanup does not match recovered outcome")
			}
		})
	}
}

func TestRequestBoundarySQLRecoveryKeepsPersistedAdmissionWhileNativeSessionBusy(t *testing.T) {
	for _, operation := range []string{"primary", "individual"} {
		for _, boundary := range []string{"save", "commit", "finish"} {
			t.Run(operation+"/"+boundary, func(t *testing.T) {
				h := newAccountWireHarness(t)
				a := h.login(t, "codex", "a@example.test", "")
				b := h.login(t, "codex", "b@example.test", "")
				env := h.assign(t, "s", domain.HarnessCodex, a)

				h.guard.busy["s"] = true
				disable := failAccountSQLBoundary(t, h, boundary)
				var err error
				if operation == "primary" {
					err = h.svc.SetPrimaryWithOptions(h.ctx, b, true)
				} else {
					err = h.svc.Switch(h.ctx, "s", b)
				}
				if err == nil {
					t.Fatal("interrupted SQL boundary reported success")
				}
				state, pending, err := h.store.LoadProviderAccountState(h.ctx)
				if err != nil {
					t.Fatal(err)
				}
				if boundary == "save" {
					if pending != nil {
						t.Fatal("failed admission published an intent")
					}
					h.assertAssignment(t, "s", a, env)
				} else if pending == nil || !pending.RequestBoundary || pending.Next.Revision < state.Revision {
					t.Fatal("database lost admitted request-boundary mode")
				}
				disable()
				h.restartDaemon(t)
				h.guard.busy["s"] = true
				guardsBefore := len(h.guard.acquired)
				if err := h.svc.Recover(h.ctx); err != nil {
					t.Fatal(err)
				}
				if len(h.guard.acquired) != guardsBefore {
					t.Fatal("recovery reinterpreted admitted rebind as idle-only")
				}
				want := b
				if boundary == "save" {
					want = a
				}
				h.assertAssignment(t, "s", want, env)
				_, pending, err = h.store.LoadProviderAccountState(h.ctx)
				if err != nil || pending != nil {
					t.Fatal("recovery left unresolved journal", err)
				}
				if err = h.svc.RestoreHost(h.ctx); err != nil {
					t.Fatal("normalized replay failed", err)
				}
			})
		}
	}
}
