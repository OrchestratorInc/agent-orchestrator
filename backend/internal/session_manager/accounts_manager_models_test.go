package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountModelsRouter struct {
	*initialSelectionRouter
	catalog ports.AgentModelCatalog
	err     error
	account string
}

func (r *accountModelsRouter) AgentAccountModels(_ context.Context, _ domain.AccountsManagerProvider, id string) (ports.AgentModelCatalog, error) {
	r.account = id
	return r.catalog, r.err
}

func TestManagedLaunchUsesSelectedAccountModelCapabilities(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		t.Run(string(mode), func(t *testing.T) {
			m, _, _, initial := initialSelectionFixture()
			calls := 0
			m.modelCatalog = tuningCatalog{calls: &calls, err: errors.New("native credentials are absent")}
			router := &accountModelsRouter{initialSelectionRouter: initial, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "account-model", Efforts: []string{"low", "high"}}}}}
			m.accountsManager = router
			cfg := ports.SpawnConfig{Harness: domain.HarnessClaudeCode, Kind: domain.KindWorker, RequestedMode: mode,
				Account:     &domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-b"},
				AgentConfig: ports.AgentConfig{Model: "account-model", Effort: "high"}, EffortOverride: true}
			resolved, err := m.resolveAgentConfig(t.Context(), cfg, domain.ProjectConfig{})
			if err != nil || resolved.Model != "account-model" || resolved.Effort != "high" || router.account != "account-b" || calls != 0 {
				t.Fatalf("managed model used the wrong catalog: config=%+v error=%v account=%q nativeCalls=%d", resolved, err, router.account, calls)
			}
		})
	}
}

func TestManagedLaunchCapabilitiesFailClosed(t *testing.T) {
	for _, failure := range []string{"missing port", "read error", "stale", "unknown model", "unsupported effort"} {
		t.Run(failure, func(t *testing.T) {
			m, _, _, initial := initialSelectionFixture()
			calls := 0
			m.modelCatalog = tuningCatalog{calls: &calls, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "account-model", Efforts: []string{"high"}}}}}
			router := &accountModelsRouter{initialSelectionRouter: initial, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "account-model", Efforts: []string{"high"}}}}}
			m.accountsManager = router
			want := ports.ErrModelCapabilitiesUnavailable
			switch failure {
			case "missing port":
				m.accountsManager = initial
			case "read error":
				router.err = errors.New("unavailable")
			case "stale":
				router.catalog.Stale = true
			case "unknown model":
				router.catalog.Models = nil
				want = ErrUnsupportedModel
			case "unsupported effort":
				router.catalog.Models[0].Efforts = []string{"low"}
				want = ports.ErrUnsupportedEffort
			}
			cfg := ports.SpawnConfig{Harness: domain.HarnessClaudeCode, Account: &domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-b"}, AgentConfig: ports.AgentConfig{Model: "account-model", Effort: "high"}}
			if _, err := m.resolveAgentConfig(t.Context(), cfg, domain.ProjectConfig{}); !errors.Is(err, want) || calls != 0 {
				t.Fatalf("managed failure reached native catalog: error=%v want=%v calls=%d", err, want, calls)
			}
		})
	}
}

func TestManagedModelSpawnValidatesBeforeRuntimeCreation(t *testing.T) {
	for _, valid := range []bool{true, false} {
		m, store, runtime, initial := initialSelectionFixture()
		m.modelCatalog = tuningCatalog{err: errors.New("no native credentials")}
		router := &accountModelsRouter{initialSelectionRouter: initial, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "account-model", Efforts: []string{"high"}}}}}
		m.accountsManager = router
		cfg := initialSelectionConfig(domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-b"})
		cfg.Harness = domain.HarnessClaudeCode
		cfg.AgentConfig = ports.AgentConfig{Model: "account-model", Effort: "high"}
		if !valid {
			cfg.AgentConfig.Effort = "unknown"
		}
		_, _, _, err := m.Spawn(t.Context(), cfg)
		if valid && (err != nil || runtime.created != 1 || router.account != "account-b") {
			t.Fatalf("explicit managed model failed: %v", err)
		}
		if !valid && (!errors.Is(err, ports.ErrUnsupportedEffort) || runtime.created != 0 || len(store.sessions) != 0) {
			t.Fatalf("unsupported effort reached durable creation: %v", err)
		}
	}
}
