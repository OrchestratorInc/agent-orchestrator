package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSpawnExistingOrchestratorAccountChoice(t *testing.T) {
	for _, replay := range []string{"interactive", "automation"} {
		for _, scenario := range []string{
			"other account", "native requested", "managed requested", "missing binding", "blocked binding",
			"other session", "other provider", "invalid revision", "read error", "unsupported reader",
			"invalid choice", "other harness", "unsupported harness", "managed match", "other supported match", "native match", "omitted",
		} {
			t.Run(replay+"/"+scenario, func(t *testing.T) {
				svc, store, manager := controlServiceFixture()
				store.projects["mer"] = domain.ProjectRecord{ID: "mer"}
				store.op = domain.AccountsManagerSwitch{}
				rec := store.sessions["session-a"]
				rec.ProjectID, rec.Kind = "mer", domain.KindOrchestrator
				cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
					Account: &domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}}
				if replay == "automation" {
					run := domain.AutomationRunID("same-run")
					rec.AutomationRunID, cfg.AutomationRunID = &run, &run
				}
				wantCode := "ACCOUNT_BINDING_CHANGED"
				var readErr error
				switch scenario {
				case "other account":
					cfg.Account.AccountID = "account-b"
				case "native requested":
					cfg.Account = &domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerNative}
				case "managed requested":
					store.binding.Mode, store.binding.AccountID = domain.AccountsManagerNative, ""
				case "missing binding":
					store.binding = domain.AccountsManagerSessionRoute{}
				case "blocked binding":
					store.binding.Blocked = true
				case "other session":
					store.binding.SessionID = "foreign-session"
				case "other provider":
					store.binding.Provider = "other-provider"
				case "invalid revision":
					store.binding.Revision = 0
				case "read error":
					readErr = errors.New("synthetic binding read error")
					store.readErr = readErr
				case "unsupported reader":
					svc.store = store.fakeStore
					wantCode = "ACCOUNT_SELECTION_UNAVAILABLE"
				case "invalid choice":
					cfg.Account.AccountID = ""
					wantCode = "ACCOUNT_SELECTION_INVALID"
				case "other harness":
					cfg.Harness = domain.HarnessGemini
				case "unsupported harness":
					rec.Harness, cfg.Harness = domain.HarnessGemini, domain.HarnessGemini
					wantCode = "ACCOUNT_SELECTION_INVALID"
				case "managed match":
					wantCode = ""
				case "other supported match":
					rec.Harness, cfg.Harness = domain.HarnessClaudeCode, domain.HarnessClaudeCode
					store.binding.Provider = domain.AccountsManagerProviderClaude
					wantCode = ""
				case "native match":
					cfg.Account = &domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerNative}
					store.binding.Mode, store.binding.AccountID = domain.AccountsManagerNative, ""
					wantCode = ""
				case "omitted":
					cfg.Account, cfg.Harness, svc.store = nil, domain.HarnessGemini, store.fakeStore
					wantCode = ""
				}
				store.sessions[rec.ID] = rec
				before := store.binding
				got, promptBytes, systemBytes, err := svc.Spawn(t.Context(), cfg)
				if readErr != nil {
					if !errors.Is(err, readErr) {
						t.Errorf("reuse discarded the binding read error: %v", err)
					}
				} else if wantCode != "" {
					var typed *apierr.Error
					if !errors.As(err, &typed) || typed.Code != wantCode {
						t.Errorf("reuse ignored explicit account choice: got %v, want %s", err, wantCode)
					}
				} else if err != nil || got.ID != rec.ID {
					t.Errorf("matching or omitted choice lost idempotent reuse: %v", err)
				}
				if (wantCode != "" || readErr != nil) && got.ID != "" {
					t.Error("rejected account choice returned an existing session as success")
				}
				if manager.spawnCalls != 0 || len(manager.calls) != 0 || store.binding != before || len(store.sessions) != 1 || promptBytes != 0 || systemBytes != 0 {
					t.Fatal("reuse changed account/session state or launched provider work")
				}
			})
		}
	}
}

func TestSpawnExistingOrchestratorAccountSwitchSnapshot(t *testing.T) {
	for _, scenario := range []string{"requested", "waiting", "stopping", "stopped", "committed", "starting", "recovery_required", "ready", "cancelled", "failed", "foreign journal", "changed binding", "missing binding", "cancelled read"} {
		t.Run(scenario, func(t *testing.T) {
			svc, store, manager := controlServiceFixture()
			store.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			rec := store.sessions["session-a"]
			rec.ProjectID, rec.Kind = "mer", domain.KindOrchestrator
			store.sessions[rec.ID] = rec
			store.op.Phase = domain.AccountsManagerSwitchPhase(scenario)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "foreign journal":
				store.op.SessionID, store.op.Phase = "foreign-session", domain.AccountsManagerSwitchReady
			case "changed binding":
				store.afterRead = func() {
					store.op.Phase = domain.AccountsManagerSwitchReady
					store.binding.AccountID, store.binding.Revision = "account-b", store.binding.Revision+1
				}
			case "missing binding":
				store.afterRead = func() {
					store.op.Phase = domain.AccountsManagerSwitchReady
					store.binding = domain.AccountsManagerSessionRoute{}
				}
			case "cancelled read":
				store.op.Phase = domain.AccountsManagerSwitchReady
				store.afterRead = cancel
			}
			got, _, _, err := svc.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator,
				Account: &domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}})
			if scenario == "ready" || scenario == "cancelled" || scenario == "failed" {
				if err != nil || got.ID != rec.ID {
					t.Fatal("terminal journal rejected matching committed account", err)
				}
			} else if err == nil || got.ID != "" {
				t.Fatal("reuse accepted an unresolved or mixed account snapshot")
			}
			if manager.spawnCalls != 0 || len(manager.calls) != 0 {
				t.Fatal("reuse restarted or switched an existing session")
			}
		})
	}
}
