package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accountcore "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type controlReadStore struct {
	*fakeStore
	binding   domain.AccountsManagerSessionRoute
	op        domain.AccountsManagerSwitch
	removal   domain.AccountsManagerRemoval
	readErr   error
	afterRead func()
	provider  domain.AccountsManagerProvider
}

func (s *controlReadStore) GetAccountsManagerSessionRoute(_ context.Context, _ domain.SessionID, provider domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error) {
	s.provider = provider
	return s.binding, s.binding.SessionID != "", s.readErr
}

func (s *controlReadStore) GetAccountsManagerSwitch(context.Context, string) (domain.AccountsManagerSwitch, bool, error) {
	return s.op, s.op.ID != "", s.readErr
}

func (s *controlReadStore) GetLatestAccountsManagerSwitch(context.Context, domain.SessionID) (domain.AccountsManagerSwitch, bool, error) {
	if s.afterRead != nil {
		s.afterRead()
	}
	return s.op, s.op.ID != "", s.readErr
}

func (s *controlReadStore) AccountsManagerRemovalImpact(context.Context, string) (domain.AccountsManagerRemovalImpact, error) {
	return s.removal.Impact, s.readErr
}

func (s *controlReadStore) GetAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, bool, error) {
	return s.removal, s.removal.ID != "", s.readErr
}

type controlCommander struct {
	*fakeCommander
	calls     []string
	config    sessionmanager.AccountsManagerSwitchConfig
	operation string
	account   string
	revision  int64
	confirmed bool
	err       error
}

func (m *controlCommander) StartAccountsManagerSwitch(_ context.Context, id domain.SessionID, cfg sessionmanager.AccountsManagerSwitchConfig) (domain.AccountsManagerSwitch, error) {
	m.calls = append(m.calls, "start switch")
	m.config = cfg
	return domain.AccountsManagerSwitch{ID: cfg.OperationID, SessionID: id}, m.err
}

func (m *controlCommander) RetryAccountsManagerSwitch(_ context.Context, id domain.SessionID, op string) (domain.AccountsManagerSwitch, error) {
	m.calls = append(m.calls, "retry switch")
	return domain.AccountsManagerSwitch{ID: op, SessionID: id}, m.err
}

func (m *controlCommander) CancelAccountsManagerSwitch(_ context.Context, id domain.SessionID, op string) (domain.AccountsManagerSwitch, error) {
	m.calls = append(m.calls, "cancel switch")
	return domain.AccountsManagerSwitch{ID: op, SessionID: id}, m.err
}

func (m *controlCommander) StartAccountsManagerRemoval(_ context.Context, op, account string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	m.calls = append(m.calls, "start removal")
	m.operation, m.account, m.revision, m.confirmed = op, account, revision, confirmed
	return domain.AccountsManagerRemoval{ID: op, AccountID: account}, m.err
}

func (m *controlCommander) RetryAccountsManagerRemoval(_ context.Context, op string) (domain.AccountsManagerRemoval, error) {
	m.calls = append(m.calls, "retry removal")
	return domain.AccountsManagerRemoval{ID: op}, m.err
}

func (m *controlCommander) CancelAccountsManagerRemoval(_ context.Context, op string) (domain.AccountsManagerRemoval, error) {
	m.calls = append(m.calls, "cancel removal")
	return domain.AccountsManagerRemoval{ID: op}, m.err
}

func controlServiceFixture() (*Service, *controlReadStore, *controlCommander) {
	store := &controlReadStore{fakeStore: newFakeStore(),
		binding: domain.AccountsManagerSessionRoute{SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "account-a", Revision: 5},
		op:      domain.AccountsManagerSwitch{ID: "switch-a", SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex},
		removal: domain.AccountsManagerRemoval{ID: "remove-a", AccountID: "account-a"},
	}
	store.sessions["session-a"] = domain.SessionRecord{ID: "session-a", Harness: domain.HarnessCodex}
	manager := &controlCommander{fakeCommander: &fakeCommander{}}
	return NewWithDeps(Deps{Store: store, Manager: manager}), store, manager
}

func TestAccountsManagerControlReadRejectsMixedCommitSnapshot(t *testing.T) {
	svc, store, _ := controlServiceFixture()
	store.afterRead = func() {
		store.binding.AccountID, store.binding.Revision = "account-b", 6
		store.op.Phase, store.op.TargetRevision = domain.AccountsManagerSwitchReady, 6
	}
	_, _, err := svc.SessionAccount(t.Context(), "session-a")
	if !errors.Is(err, domain.ErrAccountsManagerBindingConflict) {
		t.Fatalf("combined an old committed binding with a newer ready operation: %v", err)
	}
}

func TestAccountsManagerControlReadProviderAndMissingState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*controlReadStore)
		wantErr bool
	}{
		{"committed", func(*controlReadStore) {}, false},
		{"missing session", func(s *controlReadStore) { delete(s.sessions, "session-a") }, true},
		{"task preparation", func(s *controlReadStore) {
			r := s.sessions["session-a"]
			r.IsTaskPreparation = true
			s.sessions[r.ID] = r
		}, true},
		{"missing binding", func(s *controlReadStore) { s.binding = domain.AccountsManagerSessionRoute{} }, true},
		{"foreign binding", func(s *controlReadStore) { s.binding.SessionID = "session-b" }, true},
		{"foreign operation", func(s *controlReadStore) { s.op.SessionID = "session-b" }, true},
		{"unsupported provider", func(s *controlReadStore) {
			r := s.sessions["session-a"]
			r.Harness = domain.HarnessOpenCode
			s.sessions[r.ID] = r
		}, true},
		{"other supported provider", func(s *controlReadStore) {
			r := s.sessions["session-a"]
			r.Harness = domain.HarnessClaudeCode
			s.sessions[r.ID] = r
			s.binding.Provider, s.op.Provider = domain.AccountsManagerProviderClaude, domain.AccountsManagerProviderClaude
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, manager := controlServiceFixture()
			tc.mutate(store)
			got, pending, err := svc.SessionAccount(t.Context(), "session-a")
			if (err != nil) != tc.wantErr || len(manager.calls) != 0 {
				t.Fatalf("read error=%v, mutation calls=%v", err, manager.calls)
			}
			if err == nil && (got != store.binding || pending == nil || pending.ID != store.op.ID || store.provider != store.binding.Provider) {
				t.Fatal("read did not preserve the actual provider and committed binding")
			}
		})
	}
}

func TestAccountsManagerControlDelegatesOnlyExplicitChoices(t *testing.T) {
	for _, mode := range []domain.AccountsManagerConnectionMode{domain.AccountsManagerManaged, domain.AccountsManagerNative} {
		for _, policy := range []domain.SessionInterfaceTransitionPolicy{domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionInterrupt} {
			t.Run(string(mode)+"/"+string(policy), func(t *testing.T) {
				svc, _, manager := controlServiceFixture()
				account := ""
				if mode == domain.AccountsManagerManaged {
					account = "account-b"
				}
				input := domain.AccountsManagerSwitch{ID: "new-operation", SessionID: "session-a", SourceRevision: 5,
					TargetMode: mode, TargetAccountID: account, Policy: policy, NewConversation: true,
					TargetGeneration: "untrusted-generation", SourceRuntimeHandleID: "untrusted-handle"}
				if _, err := svc.StartAccountSwitch(t.Context(), input); err != nil {
					t.Fatal(err)
				}
				want := sessionmanager.AccountsManagerSwitchConfig{OperationID: input.ID, ExpectedRevision: 5, Mode: mode, AccountID: account, Policy: policy, NewConversation: true}
				if manager.config != want || len(manager.calls) != 1 {
					t.Fatal("explicit choice was changed or controller ownership bypassed")
				}
			})
		}
	}
}

func TestAccountsManagerControlMutationOwnership(t *testing.T) {
	for _, kind := range []string{"retry switch", "cancel switch", "retry removal", "cancel removal"} {
		for _, wrong := range []string{"", "missing", "foreign"} {
			t.Run(kind+"/"+wrong, func(t *testing.T) {
				svc, store, manager := controlServiceFixture()
				switch wrong {
				case "missing":
					store.op.ID, store.removal.ID = "", ""
				case "foreign":
					store.op.SessionID, store.removal.ID = "session-b", "remove-b"
				}
				var err error
				switch kind {
				case "retry switch":
					_, err = svc.RetryAccountSwitch(t.Context(), "session-a", "switch-a")
				case "cancel switch":
					_, err = svc.CancelAccountSwitch(t.Context(), "session-a", "switch-a")
				case "retry removal":
					_, err = svc.RetryAccountRemoval(t.Context(), "remove-a")
				case "cancel removal":
					_, err = svc.CancelAccountRemoval(t.Context(), "remove-a")
				}
				if wrong != "" {
					var typed *apierr.Error
					if !errors.As(err, &typed) || typed.Kind != apierr.KindNotFound || len(manager.calls) != 0 {
						t.Fatalf("ownership rejection=%v calls=%v", err, manager.calls)
					}
				} else if err != nil || len(manager.calls) != 1 || manager.calls[0] != kind {
					t.Fatalf("delegation=%v calls=%v", err, manager.calls)
				}
			})
		}
	}
}

func TestAccountsManagerControlRemovalRevisionAndUnavailable(t *testing.T) {
	svc, _, manager := controlServiceFixture()
	if _, err := svc.StartAccountRemoval(t.Context(), "remove-zero", "account-a", 0, true); err != nil {
		t.Fatal(err)
	}
	if manager.operation != "remove-zero" || manager.account != "account-a" || manager.revision != 0 || !manager.confirmed {
		t.Fatal("removal changed explicit zero revision or confirmation")
	}
	manager.err = fmt.Errorf("private endpoint: %w", accountcore.ErrUnavailable)
	if _, err := svc.StartAccountRemoval(t.Context(), "remove-zero", "account-a", 0, true); !errors.Is(err, accountcore.ErrUnavailable) || err.Error() != accountcore.ErrUnavailable.Error() {
		t.Fatalf("unavailable error changed category or retained private details: %v", err)
	}
	var typed *apierr.Error
	if _, _, err := (&Service{}).SessionAccount(t.Context(), "session-a"); !errors.As(err, &typed) || typed.Kind != apierr.KindNotImplemented {
		t.Fatalf("missing capabilities: %v", err)
	}
}
