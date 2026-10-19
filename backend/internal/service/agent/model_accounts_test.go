package agent

import (
	"context"
	"encoding/json"
	"testing"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type modelAccountEnvs struct{ defaultID string }

func (a *modelAccountEnvs) CatalogLaunchEnv(_ context.Context, _ domain.AgentHarness, session domain.SessionID, account string) (string, map[string]string, error) {
	if account == "" {
		account = a.defaultID
		if session == "saved-session" {
			account = "fast"
		}
	}
	return account, map[string]string{"CODEX_HOME": "/accounts/" + account}, nil
}

type accountTierDiscoverer struct{ fakeModelDiscoverer }

func (d *accountTierDiscoverer) Discover(_ context.Context, req ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	tiers := []ports.ModelServiceTier{}
	if req.Env["CODEX_HOME"] == "/accounts/fast" {
		tiers = []ports.ModelServiceTier{{ID: "fast"}}
	}
	return ports.AgentModelCatalog{AgentID: "codex", Models: []ports.AgentModelInfo{{ID: "gpt-5", ServiceTiers: tiers}}}, nil
}
func (*accountTierDiscoverer) CatalogFingerprint(_ context.Context, req ports.AgentModelDiscoveryRequest) string {
	return req.Env["CODEX_HOME"]
}

func TestModelCatalogsStayScopedToDefaultSessionAndTargetAccounts(t *testing.T) {
	ctx := context.Background()
	accounts := &modelAccountEnvs{defaultID: "fast"}
	cache := &fakeModelCache{}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, nil, &accountTierDiscoverer{})
	svc.SetAccountModelEnvironment(accounts)
	assertTiers := func(c ports.AgentModelCatalog, err error, fast bool) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Models) != 1 || (len(c.Models[0].ServiceTiers) > 0) != fast {
			t.Fatalf("catalog = %#v, want fast=%v", c, fast)
		}
	}
	c, err := svc.Models(ctx, "codex", "", false)
	assertTiers(c, err, true)
	accounts.defaultID = "normal"
	c, err = svc.Models(ctx, "codex", "", false)
	assertTiers(c, err, false)
	c, err = svc.ModelsForSession(ctx, "codex", "", "saved-session", "", false)
	assertTiers(c, err, true)
	c, err = svc.ModelsForSession(ctx, "codex", "", "saved-session", "normal", false)
	assertTiers(c, err, false)
	accounts.defaultID = "fast"
	c, err = svc.Models(ctx, "codex", "", false)
	assertTiers(c, err, true)
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if len(cache.records) != 2 {
		t.Fatalf("cached account scopes=%d want 2", len(cache.records))
	}
	for _, record := range cache.records {
		scope, ok := decodeAccountModelScope(record.ProjectID)
		if !ok {
			t.Fatalf("not an account scope: %s", record.ProjectID)
		}
		var metadata map[string]string
		if err := json.Unmarshal([]byte(record.MetadataJSON), &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata["accountId"] != scope.AccountID || metadata["projectId"] != scope.ProjectID {
			t.Fatalf("CDC metadata=%v scope=%+v", metadata, scope)
		}
	}
}
