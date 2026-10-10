package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type serviceAccountRouting struct {
	managed    bool
	resolveErr error
	calls      []ports.SpawnConfig
}

func (a *serviceAccountRouting) ResolveAccount(_ context.Context, harness domain.AgentHarness, id string) (string, bool, error) {
	a.calls = append(a.calls, ports.SpawnConfig{Harness: harness, ProviderAccountID: id})
	return "resolved-primary", a.managed, a.resolveErr
}
func (*serviceAccountRouting) AssignAccount(context.Context, domain.SessionID, domain.AgentHarness, string) error {
	return nil
}
func (*serviceAccountRouting) SessionAccount(context.Context, domain.SessionID) (domain.ProviderSessionRoute, bool, error) {
	return domain.ProviderSessionRoute{}, false, nil
}
func (*serviceAccountRouting) LaunchAccountEnv(context.Context, domain.SessionID) (map[string]string, error) {
	return nil, nil
}
func TestManagedAccountSpawnChecksInstallationAndIgnoresDeviceAuthentication(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		for _, authentication := range []domain.AgentAuthenticationState{domain.AgentAuthenticationAuthorized, domain.AgentAuthenticationUnauthorized, domain.AgentAuthenticationUnknown} {
			for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
				t.Run(fmt.Sprintf("%s/%s/%s", harness, authentication, mode), func(t *testing.T) {
					store := newFakeStore()
					store.projects["project"] = domain.ProjectRecord{ID: "project"}
					manager := &fakeCommander{}
					routing := &serviceAccountRouting{managed: true}
					readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
						ID: string(harness), Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled, Freshness: domain.AgentReadinessFresh},
						Authentication: domain.AgentAuthenticationObservation{State: authentication, Freshness: domain.AgentReadinessFresh},
					}}
					service := NewWithDeps(Deps{Manager: manager, Store: store, AgentReadiness: readiness})
					service.SetProviderAccounts(routing)
					selection := "explicit-account"
					_, _, _, err := service.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "project", Kind: domain.KindWorker, Harness: harness, RequestedMode: mode, ProviderAccountID: selection, DisplayName: "managed worker"})
					if err != nil {
						t.Fatal(err)
					}
					if manager.spawnCalls != 1 || manager.spawnedCfg.ProviderAccountID != selection || manager.spawnedCfg.Harness != harness || manager.spawnedCfg.RequestedMode != mode {
						t.Fatalf("manager spawn=%+v", manager.spawnedCfg)
					}
					if len(routing.calls) != 1 || routing.calls[0].ProviderAccountID != selection || routing.calls[0].Harness != harness {
						t.Fatalf("routing preflight=%+v", routing.calls)
					}
					if readiness.calls != 1 || readiness.agentID != string(harness) || readiness.purpose != domain.AgentReadinessPurposeLaunch {
						t.Fatal("managed routing bypassed executable readiness")
					}
					if len(readiness.authInvalidated) != 0 || len(readiness.installationInvalidated) != 0 {
						t.Fatal("managed credentials changed native device readiness")
					}
				})
			}
		}
	}
}
func TestManagedAccountSpawnCannotBypassMissingExecutable(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		t.Run(string(harness), func(t *testing.T) {
			store := newFakeStore()
			store.projects["project"] = domain.ProjectRecord{ID: "project"}
			manager := &fakeCommander{}
			routing := &serviceAccountRouting{managed: true}
			readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{ID: string(harness), Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled}, Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized}}}
			service := NewWithDeps(Deps{Manager: manager, Store: store, AgentReadiness: readiness})
			service.SetProviderAccounts(routing)
			_, _, _, err := service.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "project", Harness: harness, Kind: domain.KindWorker, ProviderAccountID: "account-a"})
			var apiError *apierr.Error
			if !errors.As(err, &apiError) || apiError.Code != "AGENT_BINARY_NOT_FOUND" {
				t.Fatalf("missing executable=%v", err)
			}
			if manager.spawnCalls != 0 {
				t.Fatal("managed account launch ran without installed CLI")
			}
			if len(routing.calls) != 1 || readiness.calls != 1 {
				t.Fatal("managed preflight skipped routing or installation")
			}
		})
	}
}
func TestManagedProviderSpawnDoesNotFallBackToNativeAuthentication(t *testing.T) {
	store := newFakeStore()
	store.projects["project"] = domain.ProjectRecord{ID: "project"}
	manager := &fakeCommander{}
	routing := &serviceAccountRouting{managed: true, resolveErr: ports.ErrProviderLoginRequired}
	readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}, Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh}}}
	service := NewWithDeps(Deps{Manager: manager, Store: store, AgentReadiness: readiness})
	service.SetProviderAccounts(routing)
	_, _, _, err := service.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "project", Harness: domain.HarnessCodex, Kind: domain.KindWorker})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "PROVIDER_LOGIN_REQUIRED" {
		t.Fatalf("managed login error=%v", err)
	}
	if manager.spawnCalls != 0 || len(routing.calls) != 1 {
		t.Fatal("managed provider fell back to native launch")
	}
}
func TestManagedAccountSpawnRoutingErrorsReachAPIWithoutFallback(t *testing.T) {
	cases := []struct {
		failure error
		code    string
	}{
		{ports.ErrProviderLoginRequired, "PROVIDER_LOGIN_REQUIRED"},
		{ports.ErrProviderAccountUnknown, "PROVIDER_ACCOUNT_NOT_FOUND"},
		{ports.ErrProviderAccountIncompatible, "PROVIDER_ACCOUNT_INCOMPATIBLE"},
		{ports.ErrProviderAccountRecovery, "PROVIDER_ACCOUNT_RECOVERY_REQUIRED"},
		{ports.ErrProviderAccountBusy, "PROVIDER_ACCOUNT_IN_USE"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			store := newFakeStore()
			store.projects["project"] = domain.ProjectRecord{ID: "project"}
			manager := &fakeCommander{}
			routing := &serviceAccountRouting{managed: true, resolveErr: fmt.Errorf("managed route: %w", tc.failure)}
			readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{ID: "codex"}}
			service := NewWithDeps(Deps{Manager: manager, Store: store, AgentReadiness: readiness})
			service.SetProviderAccounts(routing)
			_, _, _, err := service.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "project", Harness: domain.HarnessCodex, Kind: domain.KindWorker, ProviderAccountID: "selected"})
			var apiError *apierr.Error
			if !errors.As(err, &apiError) || apiError.Code != tc.code {
				t.Fatalf("account failure envelope=%v", err)
			}
			if manager.spawnCalls != 0 || readiness.calls != 0 {
				t.Fatal("routing error fell back to native launch or device auth")
			}
			if len(store.sessions) != 0 {
				t.Fatal("account admission error created session")
			}
		})
	}
}
func TestManagedAccountStandaloneSpawnPreservesExplicitAndDefaultSelection(t *testing.T) {
	for _, selection := range []string{"", "account-a"} {
		t.Run(selection, func(t *testing.T) {
			store := newFakeStore()
			manager := &fakeCommander{}
			routing := &serviceAccountRouting{managed: true}
			service := NewWithDeps(Deps{Manager: manager, Store: store})
			service.SetProviderAccounts(routing)
			_, _, _, err := service.Spawn(context.Background(), ports.SpawnConfig{Harness: domain.HarnessClaudeCode, Kind: domain.KindWorker, ProviderAccountID: selection, DisplayName: "standalone"})
			if err != nil {
				t.Fatal(err)
			}
			if manager.spawnCalls != 1 || manager.spawnedCfg.ProjectID != "" || manager.spawnedCfg.ProviderAccountID != selection {
				t.Fatalf("standalone account spawn=%+v", manager.spawnedCfg)
			}
			if len(routing.calls) != 1 || routing.calls[0].ProviderAccountID != selection {
				t.Fatal("standalone launch did not resolve provider account")
			}
		})
	}
}
func TestManagedAccountDelegateForwardsSelectionWithoutChangingTaskSettings(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, ""} {
		for _, selection := range []string{"", "account-a"} {
			t.Run(string(harness)+selection, func(t *testing.T) {
				store := newFakeStore()
				store.projects["project"] = domain.ProjectRecord{ID: "project"}
				manager := &fakeCommander{}
				service := NewWithDeps(Deps{Manager: manager, Store: store})
				effort := "high"
				input := DelegateTaskInput{ProjectID: "project", RequestedAgent: harness, ProviderAccountID: selection, RequestedMode: domain.SessionModeChat, TaskPreparation: "prepared-worktree", Model: "model-probe", Effort: &effort, ApprovalMode: domain.PermissionMode("auto")}
				outcome, err := service.DelegateTask(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				if outcome.WorkerID == "" || outcome.OrchestratorID != "" {
					t.Fatalf("delegation outcome=%+v", outcome)
				}
				cfg := manager.spawnedCfg
				if cfg.ProviderAccountID != selection || cfg.Harness != harness || cfg.ProjectID != "project" || cfg.Kind != domain.KindWorker {
					t.Fatalf("delegated account choice=%+v", cfg)
				}
				if cfg.AgentConfig.Model != "model-probe" || cfg.AgentConfig.Effort != "high" || !cfg.EffortOverride || cfg.AgentConfig.Permissions != ports.PermissionMode("auto") {
					t.Fatal("account selection lost task tuning")
				}
				if cfg.RequestedMode != domain.SessionModeChat || cfg.TaskPreparation != "prepared-worktree" || !cfg.Async {
					t.Fatal("account selection changed desktop spawn lifecycle")
				}
				if len(manager.backgroundCalls) != 0 || len(manager.sent) != 0 {
					t.Fatal("promptless managed delegation submitted an extra provider request")
				}
			})
		}
	}
}
func TestManagedAccountDelegateFailurePreservesAccountError(t *testing.T) {
	for _, failure := range []error{ports.ErrProviderLoginRequired, ports.ErrProviderAccountBusy, ports.ErrProviderAccountIncompatible, ports.ErrProviderAccountUnknown, ports.ErrProviderAccountRecovery} {
		t.Run(failure.Error(), func(t *testing.T) {
			store := newFakeStore()
			store.projects["project"] = domain.ProjectRecord{ID: "project"}
			manager := &fakeCommander{spawnErr: fmt.Errorf("account admission: %w", failure)}
			service := NewWithDeps(Deps{Manager: manager, Store: store})
			outcome, err := service.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "project", RequestedAgent: domain.HarnessCodex, ProviderAccountID: "account-a", Brief: "Fix a bug"})
			var apiError *apierr.Error
			if !errors.As(err, &apiError) || apiError.Code == "" {
				t.Fatalf("delegation error=%v", err)
			}
			mapped := toAPIError(failure)
			var want *apierr.Error
			if !errors.As(mapped, &want) || apiError.Code != want.Code {
				t.Fatalf("delegation changed account error=%s want=%v", apiError.Code, mapped)
			}
			if !reflect.DeepEqual(outcome, DelegateTaskOutcome{}) || manager.spawnCalls != 0 || len(manager.backgroundCalls) != 0 {
				t.Fatal("failed account admission created or titled a worker")
			}
		})
	}
}
