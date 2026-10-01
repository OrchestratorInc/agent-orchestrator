package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type managedCodexRegistry struct {
	fakeRegistry
	managed ports.ChatDriver
}

func TestManagedCodexPreflightDoesNotUseNativeProbe(t *testing.T) {
	probes := 0
	nativeProbes := 0
	svc := chatsvc.New(chatsvc.Options{Drivers: managedCodexRegistry{
		fakeRegistry: fakeRegistry{driver: fakeDriver{probe: func() error {
			nativeProbes++
			return ports.ErrChatAuthRequired
		}}},
		managed: fakeDriver{probe: func() error {
			probes++
			return nil
		}},
	}})
	if err := svc.PreflightManagedChat(t.Context(), domain.HarnessCodex, ports.PermissionModeDefault); err != nil || probes != 1 || nativeProbes != 0 {
		t.Fatal("managed preflight did not use its driver", err)
	}
	if err := svc.PreflightManagedChat(t.Context(), domain.HarnessCodex, ports.PermissionModeDefault); err != nil || probes != 1 {
		t.Fatal("successful managed probe was not cached", err)
	}
	if err := svc.PreflightChat(t.Context(), domain.HarnessCodex, ports.PermissionModeDefault); !errors.Is(err, ports.ErrChatAuthRequired) || nativeProbes != 1 {
		t.Fatal("managed capability cache bypassed native authentication", err)
	}
}

func TestManagedCodexRouteFailureNeverStartsNative(t *testing.T) {
	for _, scenario := range []string{"unavailable", "missing", "revision", "changed-before-birth"} {
		t.Run(scenario, func(t *testing.T) {
			st := openStore(t)
			calls := 0
			svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, NewID: uuid.NewString,
				Drivers: managedCodexRegistry{
					fakeRegistry: fakeRegistry{driver: fakeDriver{start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
						t.Fatal("route error started native provider")
						return nil, nil
					}}},
					managed: fakeDriver{start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
						t.Fatal("invalid route started managed provider")
						return nil, nil
					}},
				},
				AccountsManager: managedCodexRoutes{fakeChatAccountsManager: fakeChatAccountsManager{codexPinned: true},
					prepare: func(context.Context, domain.SessionID) (*ports.AccountsManagerLaunchRoute, error) {
						calls++
						route := &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:43127", Token: "synthetic-route", BindingRevision: 1}
						switch scenario {
						case "unavailable":
							return nil, errors.New("unavailable")
						case "missing":
							return nil, nil
						case "revision":
							route.BindingRevision = 0
						case "changed-before-birth":
							route.BindingRevision = int64(calls)
						}
						return route, nil
					}},
			})
			_, err := svc.Start(t.Context(), chatsvc.StartConfig{SessionID: testSession, Harness: domain.HarnessCodex,
				PrepareControllerEnv: func(context.Context, domain.SessionControllerOwner) (map[string]string, error) { return nil, nil },
			})
			if err == nil {
				t.Fatal("invalid route admitted")
			}
		})
	}
}

type managedOwnedConversation struct {
	*fakeConversation
	identity string
}

func (*managedOwnedConversation) PreservesProviderOnClose() bool { return true }
func (c *managedOwnedConversation) HostIdentity() string         { return c.identity }

func TestManagedCodexRecordsCorrectProviderAndStopsCapturedOwner(t *testing.T) {
	st := openStore(t)
	if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{
		SessionID: testSession, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "account-a",
	}); err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("ab", 32)
	stops := 0
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, NewID: uuid.NewString,
		Drivers:          managedCodexRegistry{managed: fakeDriver{conv: &managedOwnedConversation{fakeConversation: newFakeConversation(), identity: identity}}},
		AccountsManager:  managedCodexRoutes{fakeChatAccountsManager: fakeChatAccountsManager{codexPinned: true}},
		StopProviderHost: func(context.Context, domain.SessionID) error { t.Fatal("managed stop used broad teardown"); return nil },
		StopBoundProviderHost: func(_ context.Context, id domain.SessionID, got string) error {
			if id != testSession || got != identity {
				t.Fatal("wrong captured owner")
			}
			stops++
			return nil
		},
	})
	controller, err := svc.Start(t.Context(), chatsvc.StartConfig{SessionID: testSession, Harness: domain.HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := st.GetAccountsManagerChatHost(t.Context(), testSession, controller.Generation())
	if err != nil || !found || got != identity {
		t.Fatal("host owner was not recorded against managed binding", err)
	}
	record, found, err := st.GetSession(t.Context(), testSession)
	if err != nil || !found {
		t.Fatal(err)
	}
	record.Metadata.ControllerGeneration = controller.Generation()
	if err := st.UpdateSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := controller.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(t.Context(), testSession); err != nil || stops != 1 {
		t.Fatal("detached owner stop", err, stops)
	}
	project, found, err := st.GetProject(t.Context(), string(testProject))
	if err != nil || !found {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	cold := chatsvc.New(chatsvc.Options{Store: reopened, Sessions: reopened,
		AccountsManager:  managedCodexRoutes{fakeChatAccountsManager: fakeChatAccountsManager{codexPinned: true}},
		StopProviderHost: func(context.Context, domain.SessionID) error { t.Fatal("cold stop used broad teardown"); return nil },
		StopBoundProviderHost: func(_ context.Context, id domain.SessionID, got string) error {
			if id != testSession || got != identity {
				t.Fatal("SQLite reopen lost host identity")
			}
			stops++
			return nil
		},
	})
	if err := cold.Stop(t.Context(), testSession); err != nil || stops != 2 {
		t.Fatal("cold stop retry", err, stops)
	}
}

type managedCodexRoutes struct {
	fakeChatAccountsManager
	prepare func(context.Context, domain.SessionID) (*ports.AccountsManagerLaunchRoute, error)
}

func (r managedCodexRoutes) PrepareAgentLaunchRoute(ctx context.Context, id domain.SessionID, _ domain.AccountsManagerProvider, _ string) (*ports.AccountsManagerLaunchRoute, error) {
	if r.prepare != nil {
		return r.prepare(ctx, id)
	}
	return &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:43127", Token: "opaque-route-token", BindingRevision: 1}, nil
}

func (r managedCodexRegistry) ManagedDriver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	if harness != domain.HarnessCodex || r.managed == nil {
		return nil, ports.ErrChatUnsupported
	}
	return r.managed, nil
}

func TestManagedCodexChatUsesSelectedDriverAndGeneration(t *testing.T) {
	st := openStore(t)
	var started ports.ChatStartConfig
	nativeCalls := 0
	native := fakeDriver{start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
		nativeCalls++
		return nil, errors.New("managed launch reached native driver")
	}}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, NewID: uuid.NewString,
		Drivers: managedCodexRegistry{
			fakeRegistry: fakeRegistry{driver: native},
			managed:      fakeDriver{conv: newFakeConversation(), startCfg: &started},
		},
		AccountsManager: managedCodexRoutes{fakeChatAccountsManager: fakeChatAccountsManager{codexPinned: true}},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	controller, err := svc.Start(t.Context(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		DataDir: t.TempDir(), WorkspacePath: t.TempDir(), ControllerGeneration: "managed-owner",
	})
	if err != nil {
		t.Fatal("explicit managed Chat launch rejected", err)
	}
	if nativeCalls != 0 || started.Route == nil || started.Route.BaseURL != "http://127.0.0.1:43127" ||
		started.Route.TokenEnv != "AO_ACCOUNTS_MANAGER_SESSION_TOKEN" ||
		started.Env[started.Route.TokenEnv] != "opaque-route-token" {
		t.Fatal("managed launch lost its selected route or used the native driver")
	}
	if started.ControllerGeneration != "managed-owner" || controller.Generation() != started.ControllerGeneration {
		t.Fatal("provider process and durable controller used different generations")
	}
}
