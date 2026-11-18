package sessionmanager

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// codexAccountsFake resolves saved Codex accounts to their homes; "" is the
// account new sessions start on.
type codexAccountsFake struct {
	ports.AgentAccountLaunchEnv
	homes map[string]string
}

func (f codexAccountsFake) AccountHome(_ context.Context, _ domain.AgentHarness, accountID string) (string, error) {
	home, ok := f.homes[accountID]
	if !ok {
		return "", ports.ErrAgentAccountUnknown
	}
	return home, nil
}

// refreshCountingCatalog serves one catalog and counts rediscoveries.
type refreshCountingCatalog struct {
	catalog   ports.AgentModelCatalog
	mu        sync.Mutex
	refreshes int
}

func (c *refreshCountingCatalog) Models(_ context.Context, _, _ string, refresh bool) (ports.AgentModelCatalog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if refresh {
		c.refreshes++
	}
	return c.catalog, nil
}

func (c *refreshCountingCatalog) refreshCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshes
}

func codexSpawn(m *Manager, accountID, model string, project domain.ProjectConfig) (ports.AgentConfig, error) {
	return m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex, AgentAccountID: accountID,
		AgentConfig: ports.AgentConfig{Model: model},
	}, project)
}

// A Codex spawn on a model Codex already refused for its account fails at
// spawn and names the models the account lists, instead of starting a worker
// whose first turn fails (#4518). A spawn on another account, whose plan may
// include the model, is not refused; nor is a model Codex never refused (#8119).
func TestResolveAgentConfigRefusesACodexModelOnlyForTheAccountThatRefusedIt(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
	dir := t.TempDir()
	work, personal := filepath.Join(dir, "work"), filepath.Join(dir, "personal")
	if err := codexagent.DefaultUnavailableModels().Record(work, "gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	catalog := &refreshCountingCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gpt-6.1-sol"}, {ID: "gpt-5.6-sol"},
	}}}
	m := &Manager{
		backgroundContext: context.Background(), modelCatalog: catalog,
		agentAccounts: codexAccountsFake{homes: map[string]string{"": work, "work": work, "personal": personal}},
	}

	for _, accountID := range []string{"work", ""} {
		_, err := codexSpawn(m, accountID, "gpt-5.2-codex", domain.ProjectConfig{})
		if !errors.Is(err, ErrUnsupportedModel) {
			t.Fatalf("refused model on the refusing account (%q) error = %v, want ErrUnsupportedModel", accountID, err)
		}
		if !strings.Contains(err.Error(), "gpt-6.1-sol") || !strings.Contains(err.Error(), "gpt-5.6-sol") {
			t.Fatalf("error = %q, want the models the account lists", err)
		}
	}
	if _, err := codexSpawn(m, "personal", "gpt-5.2-codex", domain.ProjectConfig{}); err != nil {
		t.Fatalf("spawn on another account: error = %v, want the launch left to the CLI", err)
	}
	if _, err := codexSpawn(m, "work", "gpt-custom", domain.ProjectConfig{}); err != nil {
		t.Fatalf("unlisted model Codex never refused: error = %v, want launch", err)
	}
	if _, err := codexSpawn(m, "missing", "gpt-5.2-codex", domain.ProjectConfig{}); err != nil {
		t.Fatalf("unresolvable account: error = %v, want no refusal", err)
	}
}

// Without saved accounts the session runs on the computer's own login, whose
// home a project may still point elsewhere with CODEX_HOME.
func TestResolveAgentConfigScopesTheComputersOwnCodexLoginByProjectHome(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
	projectHome := filepath.Join(t.TempDir(), "project-codex")
	if err := codexagent.DefaultUnavailableModels().Record(projectHome, "gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	m := &Manager{backgroundContext: context.Background(), modelCatalog: &refreshCountingCatalog{}}
	pointed := domain.ProjectConfig{Env: map[string]string{"CODEX_HOME": projectHome}}
	if _, err := codexSpawn(m, "", "gpt-5.2-codex", pointed); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("project home that refused the model: error = %v, want ErrUnsupportedModel", err)
	}
	if _, err := codexSpawn(m, "", "gpt-5.2-codex", domain.ProjectConfig{}); err != nil {
		t.Fatalf("the computer's default home never refused it: error = %v, want launch", err)
	}
}

// A stale catalog that still lists a refused model proves nothing, so the
// refusal stays; a fresh listing allows the model and forgets the refusal. A
// refused spawn starts at most one rediscovery per interval.
func TestResolveAgentConfigTrustsOnlyAFreshCatalogToLiftARefusal(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
	home := filepath.Join(t.TempDir(), "work")
	if err := codexagent.DefaultUnavailableModels().Record(home, "gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	catalog := &refreshCountingCatalog{catalog: ports.AgentModelCatalog{Stale: true, Models: []ports.AgentModelInfo{
		{ID: "gpt-6.1-sol"}, {ID: "gpt-5.2-codex"},
	}}}
	m := &Manager{
		backgroundContext: context.Background(), modelCatalog: catalog,
		agentAccounts: codexAccountsFake{homes: map[string]string{"": home}},
	}
	for range 3 {
		if _, err := codexSpawn(m, "", "gpt-5.2-codex", domain.ProjectConfig{}); !errors.Is(err, ErrUnsupportedModel) {
			t.Fatalf("stale listing: error = %v, want the refusal kept", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for catalog.refreshCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := catalog.refreshCount(); got != 1 {
		t.Fatalf("rediscoveries after 3 refused spawns = %d, want 1", got)
	}

	// A fresh listing observed before the refusal proves nothing either.
	catalog.mu.Lock()
	catalog.catalog.Stale = false
	catalog.catalog.ValidatedAt = time.Now().Add(-time.Hour)
	catalog.mu.Unlock()
	if _, err := codexSpawn(m, "", "gpt-5.2-codex", domain.ProjectConfig{}); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("listing observed before the refusal: error = %v, want the refusal kept", err)
	}

	catalog.mu.Lock()
	catalog.catalog.ValidatedAt = time.Now().Add(time.Minute)
	catalog.mu.Unlock()
	if _, err := codexSpawn(m, "", "gpt-5.2-codex", domain.ProjectConfig{}); err != nil {
		t.Fatalf("fresh listing: error = %v, want launch", err)
	}
	if codexagent.DefaultUnavailableModels().Unavailable(home, "gpt-5.2-codex") {
		t.Fatal("a model a fresh catalog lists must be forgotten")
	}
}
