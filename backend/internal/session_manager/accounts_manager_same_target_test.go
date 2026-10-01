package sessionmanager

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type sameTargetRouter struct {
	ports.AccountsManagerLaunchRouter
	ports.AccountsManagerSwitchRouter
	t *testing.T
}

func (r sameTargetRouter) AdmitAgentAccountSwitch(context.Context, domain.AccountsManagerSwitch, string) (domain.AccountsManagerSwitch, bool, error) {
	r.t.Fatal("same target reached provider admission")
	return domain.AccountsManagerSwitch{}, false, nil
}

func (r sameTargetRouter) SynchronizeAgentBindings(context.Context) error {
	r.t.Fatal("same target synchronized provider bindings")
	return nil
}

type sameTargetStore struct {
	*sqlite.Store
	routeRead func() (domain.AccountsManagerSessionRoute, bool, error)
	latestErr error
}

func (s sameTargetStore) GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error) {
	return s.routeRead()
}

func (s sameTargetStore) GetLatestAccountsManagerSwitch(ctx context.Context, id domain.SessionID) (domain.AccountsManagerSwitch, bool, error) {
	if s.latestErr != nil {
		return domain.AccountsManagerSwitch{}, false, s.latestErr
	}
	return s.Store.GetLatestAccountsManagerSwitch(ctx, id)
}

type sameTargetChat struct {
	ChatLauncher
	accountsManagerChatHandoff
}

type sameTargetLaunchRegistry struct {
	*fakeRuntime
	read func() ([]ports.RuntimeHandle, error)
}

func (r sameTargetLaunchRegistry) LaunchHandles(domain.SessionID) ([]ports.RuntimeHandle, error) {
	return r.read()
}

func sameTargetDataVersion(t *testing.T, path string) func() int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(path, "ao.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return func() int {
		t.Helper()
		var version int
		if err := db.QueryRowContext(t.Context(), "PRAGMA data_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
}

func TestAccountsManagerSameTargetNoMutations(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		for _, connection := range []domain.AccountsManagerConnectionMode{domain.AccountsManagerManaged, domain.AccountsManagerNative} {
			for _, stale := range []bool{false, true} {
				t.Run(string(mode)+"/"+string(connection)+map[bool]string{true: "/stale", false: "/current"}[stale], func(t *testing.T) {
					m, st, rt, _, rec, cfg := accountSwitchFixtureMode(t, mode)
					rec.ID, rec.Harness = "", domain.HarnessCodex
					rec.Activity.State = domain.ActivityActive
					rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID = "", ""
					choice := domain.AccountsManagerAccountChoice{Mode: connection}
					if connection == domain.AccountsManagerManaged {
						choice.AccountID = "account-a"
					}
					rec, _, err := st.CreateSessionWithAccount(t.Context(), rec, choice)
					if err != nil {
						t.Fatal(err)
					}
					binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, domain.AccountsManagerProviderCodex)
					if err != nil {
						t.Fatal(err)
					}
					cfg.Mode, cfg.AccountID, cfg.ExpectedRevision = connection, choice.AccountID, binding.Revision
					if stale {
						cfg.ExpectedRevision++
					}
					m.accountsManager = sameTargetRouter{AccountsManagerLaunchRouter: m.accountsManager, t: t}
					m.chat = sameTargetChat{}
					m.runtime = sameTargetLaunchRegistry{fakeRuntime: rt, read: func() ([]ports.RuntimeHandle, error) {
						t.Fatal("same target consulted the launch registry")
						return nil, nil
					}}
					dataVersion := sameTargetDataVersion(t, rec.Metadata.WorkspacePath)
					control := dataVersion()
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					beforeVersion := dataVersion()
					if beforeVersion == control {
						t.Fatal("mutation detector missed the positive control")
					}
					var first domain.AccountsManagerSwitch
					for i := range 2 {
						op, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg)
						if err != nil {
							t.Fatal(err)
						}
						if op.ID != cfg.OperationID || op.SessionID != rec.ID || op.Provider != binding.Provider || op.Phase != domain.AccountsManagerSwitchReady ||
							op.SourceRevision != binding.Revision || op.TargetRevision != binding.Revision || op.TargetGeneration != "" ||
							op.SourceMode != binding.Mode || op.TargetMode != binding.Mode || op.SourceAccountID != binding.AccountID || op.TargetAccountID != binding.AccountID {
							t.Fatalf("unexpected no-op response: %+v", op)
						}
						if i == 0 {
							first = op
						} else if !reflect.DeepEqual(first, op) {
							t.Fatal("identical requests did not return identical responses")
						}
					}
					if dataVersion() != beforeVersion {
						t.Fatal("same target wrote durable session, binding, journal, or host state")
					}
					if _, found, err := st.GetAccountsManagerSwitch(t.Context(), cfg.OperationID); err != nil || found {
						t.Fatalf("no-op journal: found=%v err=%v", found, err)
					}
					if rt.created != 0 || rt.destroyed != 0 || rt.outputCalls != 0 || len(rt.interrupts) != 0 || len(rt.fencedRefs) != 0 {
						t.Fatal("same target touched the runtime")
					}
					if release, ok := m.AcquireSessionInput(rec.ID); !ok {
						t.Fatal("same target left input fenced")
					} else {
						release()
					}
				})
			}
		}
	}
}

func TestAccountsManagerSameTargetFences(t *testing.T) {
	for _, fence := range []string{"pending", "blocked", "operation", "terminated", "provisioning", "unreadable pending", "reused id"} {
		t.Run(fence, func(t *testing.T) {
			m, st, rt, _, rec, cfg := accountSwitchFixture(t)
			provider, _ := accountsManagerProvider(rec.Harness)
			binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, provider)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.ErrAccountsManagerSwitchConflict
			switch fence {
			case "pending", "reused id":
				op := accountHandoffJournal(t, st, rec, cfg, domain.AccountsManagerSwitchWaiting)
				if fence == "pending" {
					cfg.OperationID = op.ID + "-other"
				}
			case "blocked":
				binding.Blocked = true
				want = domain.ErrAccountsManagerBindingConflict
				m.store = sameTargetStore{Store: st, routeRead: func() (domain.AccountsManagerSessionRoute, bool, error) { return binding, true, nil }}
			case "operation":
				if err := m.beginAgentOperation(t.Context(), rec.ID, agentOperationSwitch); err != nil {
					t.Fatal(err)
				}
				defer m.endAgentOperation(rec.ID, agentOperationSwitch)
				want = errAgentOperationInProgress
			case "terminated", "provisioning":
				rec.IsTerminated = fence == "terminated"
				if fence == "provisioning" {
					rec.ProvisionState = domain.SessionProvisionProvisioning
					if changed, err := st.SetSessionProvisionState(t.Context(), rec.ID, rec.ProvisionState, "", rec.UpdatedAt); err != nil || !changed {
						t.Fatal("could not set provisioning fixture", err)
					}
				}
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				stored, err := m.getRecord(t.Context(), rec.ID)
				if err != nil || stored.Mode != rec.Mode || stored.Harness != rec.Harness || stored.Metadata.RuntimeHandleID != rec.Metadata.RuntimeHandleID {
					t.Fatal("validation fixture does not reach the intended source branch", err)
				}
			case "unreadable pending":
				want = errors.New("pending journal unavailable")
				m.store = sameTargetStore{Store: st, latestErr: want, routeRead: func() (domain.AccountsManagerSessionRoute, bool, error) { return binding, true, nil }}
			}
			cfg.AccountID = binding.AccountID
			dataVersion := sameTargetDataVersion(t, rec.Metadata.WorkspacePath)
			before := dataVersion()
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); !errors.Is(err, want) {
				t.Fatalf("fence=%s: got %v, want %v", fence, err, want)
			}
			if dataVersion() != before || rt.created != 0 || rt.destroyed != 0 {
				t.Fatal("rejected same target mutated state")
			}
		})
	}
}

func TestAccountsManagerDifferentTargetValidationPrecedence(t *testing.T) {
	lookupErr := errors.New("binding lookup unavailable")
	for _, validation := range []string{"chat", "managed chat", "metadata", "registry unsupported", "registry unreadable", "valid"} {
		for _, route := range []string{"stale", "blocked", "missing", "unreadable"} {
			t.Run(validation+"/"+route, func(t *testing.T) {
				mode := domain.SessionModeTUI
				if validation == "chat" || validation == "managed chat" {
					mode = domain.SessionModeChat
				}
				m, st, rt, _, rec, cfg := accountSwitchFixtureMode(t, mode)
				if validation == "managed chat" {
					rec.ID, rec.Harness = "", domain.HarnessCodex
					var err error
					rec, _, err = st.CreateSessionWithAccount(t.Context(), rec, domain.AccountsManagerAccountChoice{Mode: cfg.Mode, AccountID: "account-a"})
					if err != nil {
						t.Fatal(err)
					}
				}
				provider, _ := accountsManagerProvider(rec.Harness)
				binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, provider)
				if err != nil {
					t.Fatal(err)
				}
				cfg.ExpectedRevision = binding.Revision
				want := domain.ErrAccountsManagerBindingConflict
				if route == "unreadable" {
					want = lookupErr
				}
				switch validation {
				case "chat":
					rec.Mode, m.chat, want = domain.SessionModeChat, nil, ErrInterfaceHandoffUnsupported
				case "managed chat":
					rec.Mode, rec.Harness, m.chat, want = domain.SessionModeChat, domain.HarnessCodex, sameTargetChat{}, ports.ErrChatUnsupported
				case "metadata":
					rec.Metadata.RuntimeHandleID, want = "", ErrIncompleteHandle
				case "registry unsupported":
					m.runtime, want = rt, ErrInterfaceHandoffUnsupported
				case "registry unreadable":
					m.runtime = sameTargetLaunchRegistry{fakeRuntime: rt, read: func() ([]ports.RuntimeHandle, error) { return nil, errors.New("registry unavailable") }}
					want = ErrIncompleteHandle
				}
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				m.store = sameTargetStore{Store: st, routeRead: func() (domain.AccountsManagerSessionRoute, bool, error) {
					switch route {
					case "stale":
						binding.Revision = cfg.ExpectedRevision + 1
					case "blocked":
						binding.Blocked = true
					case "missing":
						return binding, false, nil
					case "unreadable":
						return binding, false, lookupErr
					}
					return binding, true, nil
				}}
				if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); !errors.Is(err, want) {
					t.Fatalf("got %v, want original validation %v", err, want)
				}
				if _, found, err := st.GetLatestAccountsManagerSwitch(t.Context(), rec.ID); err != nil || found || rt.created != 0 || rt.destroyed != 0 {
					t.Fatal("invalid target mutated state", err)
				}
			})
		}
	}
}

func TestAccountsManagerDifferentTargetRereadsBindingAfterLaunchValidation(t *testing.T) {
	m, st, rt, _, rec, cfg := accountSwitchFixture(t)
	provider, _ := accountsManagerProvider(rec.Harness)
	binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, provider)
	if err != nil {
		t.Fatal(err)
	}
	validated := false
	m.runtime = sameTargetLaunchRegistry{fakeRuntime: rt, read: func() ([]ports.RuntimeHandle, error) {
		validated = true
		return []ports.RuntimeHandle{{ID: "source"}}, nil
	}}
	m.store = sameTargetStore{Store: st, routeRead: func() (domain.AccountsManagerSessionRoute, bool, error) {
		if validated {
			binding.Revision++
		}
		return binding, true, nil
	}}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); !errors.Is(err, domain.ErrAccountsManagerBindingConflict) {
		t.Fatalf("did not read binding at original boundary: %v", err)
	}
}
