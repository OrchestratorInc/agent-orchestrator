package provideraccounts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type routingMutationScenario struct {
	name     string
	deletion bool
	arrange  func(*testing.T, accountHarness) func(*Service) error
}

func routingMutationScenarios() []routingMutationScenario {
	return []routingMutationScenario{
		{name: "first-codex-login", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			return func(s *Service) error {
				_, err := recordLogin(h.ctx, s, "codex", "first@example.test", "first.json", "first-auth", "")
				return err
			}
		}},
		{name: "second-provider-primary", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			h.login(t, "codex", "existing@example.test")
			return func(s *Service) error {
				_, err := recordLogin(h.ctx, s, "claude", "first@example.test", "claude-first.json", "claude-first-auth", "")
				return err
			}
		}},
		{name: "change-primary", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "codex", "alice@example.test")
			bob := h.login(t, "codex", "bob@example.test")
			h.assign(t, "alice-session", domain.HarnessCodex, alice)
			h.assign(t, "bob-session", domain.HarnessCodex, bob)
			return func(s *Service) error { return s.SetPrimary(h.ctx, bob) }
		}},
		{name: "assign-new-session", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "claude", "alice@example.test")
			return func(s *Service) error { return s.AssignAccount(h.ctx, "new-session", domain.HarnessClaudeCode, alice) }
		}},
		{name: "manual-codex-switch", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "codex", "alice@example.test")
			bob := h.login(t, "codex", "bob@example.test")
			h.assign(t, "switch-session", domain.HarnessCodex, alice)
			return func(s *Service) error { return s.Switch(h.ctx, "switch-session", bob) }
		}},
		{name: "manual-claude-switch", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "claude", "alice@example.test")
			bob := h.login(t, "claude", "bob@example.test")
			h.assign(t, "switch-session", domain.HarnessClaudeCode, alice)
			return func(s *Service) error { return s.Switch(h.ctx, "switch-session", bob) }
		}},
		{name: "sign-out-secondary", deletion: true, arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "codex", "alice@example.test")
			bob := h.login(t, "codex", "bob@example.test")
			h.assign(t, "primary-session", domain.HarnessCodex, alice)
			h.assign(t, "secondary-session", domain.HarnessCodex, bob)
			return func(s *Service) error { return s.Remove(h.ctx, bob, "", true) }
		}},
		{name: "remove-primary-with-replacement", deletion: true, arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "claude", "alice@example.test")
			bob := h.login(t, "claude", "bob@example.test")
			h.assign(t, "primary-session", domain.HarnessClaudeCode, alice)
			h.assign(t, "secondary-session", domain.HarnessClaudeCode, bob)
			return func(s *Service) error { return s.Remove(h.ctx, alice, bob, false) }
		}},
		{name: "last-codex-sign-out", deletion: true, arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "codex", "alice@example.test")
			claude := h.login(t, "claude", "claude@example.test")
			h.assign(t, "waiting-session", domain.HarnessCodex, alice)
			h.assign(t, "unrelated-session", domain.HarnessClaudeCode, claude)
			return func(s *Service) error { return s.Remove(h.ctx, alice, "", true) }
		}},
		{name: "last-claude-remove", deletion: true, arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "claude", "alice@example.test")
			h.assign(t, "waiting-session", domain.HarnessClaudeCode, alice)
			return func(s *Service) error { return s.Remove(h.ctx, alice, "", false) }
		}},
		{name: "relogin-restores-waiting-sessions", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "codex", "alice@example.test")
			h.assign(t, "waiting-one", domain.HarnessCodex, alice)
			h.assign(t, "waiting-two", domain.HarnessCodex, alice)
			if err := h.svc.Remove(h.ctx, alice, "", true); err != nil {
				t.Fatal(err)
			}
			return func(s *Service) error {
				_, err := recordLogin(h.ctx, s, "codex", "alice@example.test", "renewed.json", "renewed-auth", alice)
				return err
			}
		}},
		{name: "new-login-after-removing-last", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "claude", "alice@example.test")
			h.assign(t, "waiting-one", domain.HarnessClaudeCode, alice)
			if err := h.svc.Remove(h.ctx, alice, "", false); err != nil {
				t.Fatal(err)
			}
			return func(s *Service) error {
				_, err := recordLogin(h.ctx, s, "claude", "bob@example.test", "bob.json", "bob-auth", "")
				return err
			}
		}},
		{name: "delete-seed-route", arrange: func(t *testing.T, h accountHarness) func(*Service) error {
			alice := h.login(t, "codex", "alice@example.test")
			h.assign(t, "deleted", domain.HarnessCodex, alice)
			h.assign(t, "retained", domain.HarnessCodex, alice)
			return func(s *Service) error { return s.ForgetAccount(h.ctx, "deleted") }
		}},
	}
}
func TestAccountMutationRecoveryMatchesSuccessfulOperationAtEveryBoundary(t *testing.T) {
	for _, scenario := range routingMutationScenarios() {
		stages := []string{"save", "apply", "commit", "finish"}
		if scenario.deletion {
			stages = append(stages, "delete")
		}
		for _, stage := range stages {
			t.Run(scenario.name+"/"+stage, func(t *testing.T) {
				successful := setupAccounts(t)
				referenceOperation := scenario.arrange(t, successful)
				successful.svc.newID = func() string { return "next-account" }
				if err := referenceOperation(successful.svc); err != nil {
					t.Fatal(err)
				}
				expected, err := successful.svc.State(successful.ctx)
				if err != nil {
					t.Fatal(err)
				}
				expectedRoutes := clone(successful.proxy.snapshot)
				actual := setupAccounts(t)
				operation := scenario.arrange(t, actual)
				actual.svc.newID = func() string { return "next-account" }
				before, err := actual.svc.State(actual.ctx)
				if err != nil {
					t.Fatal(err)
				}
				deletedBefore := len(actual.proxy.deleted)
				if stage == "apply" || stage == "delete" {
					actual.proxy.fail = stage
				} else {
					actual.store.fail = stage
				}
				if err = operation(actual.svc); !errors.Is(err, errTestFailure) {
					t.Fatalf("fault %s was not surfaced=%v", stage, err)
				}
				actual.store.fail = ""
				actual.proxy.fail = ""
				effective, pending, err := actual.store.LoadProviderAccountState(actual.ctx)
				if err != nil {
					t.Fatal(err)
				}
				if stage == "save" {
					if pending != nil || !reflect.DeepEqual(before, effective) {
						t.Fatal("failed journal admission changed effective facts")
					}
					if len(actual.proxy.deleted) != deletedBefore {
						t.Fatal("failed admission deleted a credential")
					}
				} else {
					if pending == nil || pending.Next.Revision != before.Revision+1 {
						t.Fatal("interrupted mutation lost its durable next revision")
					}
					if stage == "apply" || stage == "commit" {
						if !reflect.DeepEqual(before, effective) {
							t.Fatal("unacknowledged routes became effective in AO")
						}
						if len(actual.proxy.deleted) != deletedBefore {
							t.Fatal("credential was deleted before route commit")
						}
					}
					if stage == "delete" || stage == "finish" {
						if !reflect.DeepEqual(expected, effective) {
							t.Fatal("acknowledged mutation was rolled back by cleanup failure")
						}
					}
				}
				if actual.guard.released != len(actual.guard.acquired) {
					t.Fatal("failed operation retained a native admission fence")
				}
				// Recreate the service to prove recovery relies on the durable intent and
				// stable ticket key rather than the original request's closure.
				restarted := New(actual.store, actual.proxy, actual.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", actual.svc.newID)
				if stage == "save" {
					err = operation(restarted)
				} else {
					err = restarted.Recover(actual.ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				effective, pending, err = actual.store.LoadProviderAccountState(actual.ctx)
				if err != nil || pending != nil {
					t.Fatal("recovery did not clear admitted journal")
				}
				if !reflect.DeepEqual(effective, expected) {
					t.Fatalf("recovered facts differ from successful %s", scenario.name)
				}
				if !reflect.DeepEqual(actual.proxy.snapshot, expectedRoutes) {
					t.Fatal("recovered helper routes differ from successful operation")
				}
				if actual.guard.released != len(actual.guard.acquired) {
					t.Fatal("recovery retained a native input fence")
				}
				for _, account := range effective.Accounts {
					if account.CredentialRef != "" {
						for _, deleted := range actual.proxy.deleted {
							if deleted == account.CredentialRef {
								t.Fatal("recovery deleted a retained account credential")
							}
						}
					}
				}
			})
		}
	}
}

type lostApplyAcknowledgement struct {
	*fakeProxy
	lose bool
}

func (p *lostApplyAcknowledgement) ApplyRoutes(ctx context.Context, next ports.ProviderRouteSnapshot) error {
	if err := p.fakeProxy.ApplyRoutes(ctx, next); err != nil {
		return err
	}
	if p.lose {
		p.lose = false
		return fmt.Errorf("helper accepted but connection closed: %w", errTestFailure)
	}
	return nil
}
func TestAccountRecoveryAfterHelperCommitButLostHTTPAcknowledgement(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "existing", domain.HarnessCodex, alice)
	before, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &lostApplyAcknowledgement{fakeProxy: h.proxy, lose: true}
	h.svc.proxy = wrapped
	if err = h.svc.Switch(h.ctx, "existing", bob); !errors.Is(err, errTestFailure) {
		t.Fatalf("lost ack=%v", err)
	}
	effective, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending == nil || !reflect.DeepEqual(before, effective) {
		t.Fatal("lost helper acknowledgement published premature AO state")
	}
	if wrapped.snapshot.Revision != pending.Next.Revision || wrapped.snapshot.Routes[0].AuthID != "bob@example.test-auth" {
		t.Fatal("fixture did not persist helper side before dropping acknowledgement")
	}
	if _, _, err = h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatal("uncertain routing allowed a new managed launch")
	}
	restarted := New(h.store, wrapped, h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { return "unused" })
	if err = restarted.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.route(t, "existing", bob)
	if h.store.pending != nil {
		t.Fatal("exact helper replay did not finish lost acknowledgement recovery")
	}
	if len(wrapped.applied) < 2 || !reflect.DeepEqual(wrapped.applied[len(wrapped.applied)-1], wrapped.applied[len(wrapped.applied)-2]) {
		t.Fatal("recovery did not replay exactly the accepted snapshot")
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("lost switch acknowledgement deleted a provider credential")
	}
}

type lostCommitAcknowledgement struct {
	*memoryStore
	lose bool
}

func (s *lostCommitAcknowledgement) CommitProviderAccountIntent(ctx context.Context, revision int64) error {
	if err := s.memoryStore.CommitProviderAccountIntent(ctx, revision); err != nil {
		return err
	}
	if s.lose {
		s.lose = false
		return fmt.Errorf("commit succeeded but result was lost: %w", errTestFailure)
	}
	return nil
}
func TestAccountRecoveryAfterSQLiteCommitButLostReturnValue(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "claude", "alice@example.test")
	bob := h.login(t, "claude", "bob@example.test")
	h.assign(t, "existing", domain.HarnessClaudeCode, alice)
	wrapped := &lostCommitAcknowledgement{memoryStore: h.store, lose: true}
	h.svc.store = wrapped
	if err := h.svc.Remove(h.ctx, alice, bob, true); !errors.Is(err, errTestFailure) {
		t.Fatalf("lost commit=%v", err)
	}
	effective, pending, err := wrapped.LoadProviderAccountState(h.ctx)
	if err != nil || pending == nil || effective.Revision != pending.Next.Revision {
		t.Fatal("fixture did not retain committed facts and unfinished cleanup")
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("commit error prematurely deleted credential")
	}
	h.guard.busy["existing"] = true
	// Committed recovery only acknowledges the already effective mapping and
	// cleans up its old credential. It need not pause a session a second time.
	restarted := New(wrapped, h.proxy, h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { return "unused" })
	if err = restarted.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.route(t, "existing", bob)
	state, err := restarted.State(h.ctx)
	if err != nil || state.Revision != effective.Revision {
		t.Fatal("recovery replay created a second routing revision")
	}
	entry, exists := account(state, alice)
	if !exists || entry.CredentialRef != "" || entry.AuthID != "" {
		t.Fatal("sign-out lost its retained catalogue entry")
	}
	if !reflect.DeepEqual(h.proxy.deleted, []string{"alice@example.test.json"}) {
		t.Fatalf("committed cleanup=%v", h.proxy.deleted)
	}
	if h.store.pending != nil {
		t.Fatal("committed recovery did not clear pending cleanup")
	}
}
