package session

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSpawnInitialAccountReadinessIsScoped(t *testing.T) {
	for _, mode := range []domain.AccountsManagerConnectionMode{domain.AccountsManagerNative, domain.AccountsManagerManaged} {
		for _, installed := range []bool{true, false} {
			name := string(mode) + "/installed"
			if !installed {
				name = string(mode) + "/missing"
			}
			t.Run(name, func(t *testing.T) {
				st := newFakeStore()
				st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
				manager := &fakeCommander{}
				readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
					ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
					Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh},
				}}
				if !installed {
					readiness.snapshot.Installation.State = domain.AgentInstallationNotInstalled
				}
				service := NewWithDeps(Deps{Manager: manager, Store: st, AgentReadiness: readiness})
				choice := &domain.AccountsManagerAccountChoice{Mode: mode}
				if mode == domain.AccountsManagerManaged {
					choice.AccountID = "account-a"
				}
				_, _, _, err := service.Spawn(t.Context(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Account: choice})
				if installed && mode == domain.AccountsManagerManaged {
					if err != nil || manager.spawnCalls != 1 || manager.spawnedCfg.Account == nil || *manager.spawnedCfg.Account != *choice {
						t.Fatal("device-global authentication blocked selected managed account admission", err)
					}
					return
				}
				var apiError *apierr.Error
				if !errors.As(err, &apiError) || manager.spawnCalls != 0 {
					t.Fatal("native auth or installation gate was bypassed", err)
				}
			})
		}
	}
}
