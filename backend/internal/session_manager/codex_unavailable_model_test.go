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
// spawn instead of starting a worker whose first turn fails (#4518), naming the
// models the catalog lists when the catalog is that account's: the computer's
// own login. A spawn on another account, whose plan may include the model, is
// not refused; nor is a model Codex never refused (#8119).
func TestResolveAgentConfigRefusesACodexModelOnlyForTheAccountThatRefusedIt(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(dir, "device"))
	work, personal := filepath.Join(dir, "work"), filepath.Join(dir, "personal")
	for _, home := range []string{work, ""} {
		if err := codexagent.DefaultUnavailableModels().Record(home, "gpt-5.2-codex", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	catalog := &refreshCountingCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gpt-6.1-sol"}, {ID: "gpt-5.6-sol"},
	}}}
	m := &Manager{
		backgroundContext: context.Background(), modelCatalog: catalog,
		agentAccounts: codexAccountsFake{homes: map[string]string{"": "", "work": work, "personal": personal}},
	}

	_, err := codexSpawn(m, "", "gpt-5.2-codex", domain.ProjectConfig{})
	if !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("refused model on the computer's own login: error = %v, want ErrUnsupportedModel", err)
	}
	if !strings.Contains(err.Error(), "gpt-6.1-sol") || !strings.Contains(err.Error(), "gpt-5.6-sol") {
		t.Fatalf("error = %q, want the models the account lists", err)
	}
	// The catalog is not the saved account's, so it names none of its models.
	_, err = codexSpawn(m, "work", "gpt-5.2-codex", domain.ProjectConfig{})
	if !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("refused model on the refusing saved account: error = %v, want ErrUnsupportedModel", err)
	}
	if strings.Contains(err.Error(), "gpt-6.1-sol") || !strings.Contains(err.Error(), "choose another model") {
		t.Fatalf("error = %q, want the plain refusal without another account's models", err)
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
	// The catalog is discovered under the computer's own login, so it can lift
	// only that login's refusals.
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "device"))
	home := ""
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

// Codex discovers its catalog under the computer's own login, never a saved
// account's. A saved account's refusal therefore stays even when a fresh
// catalog lists the model, rather than letting the spawn through to fail its
// first turn again (#4518), and the refusal names none of the catalog's models.
func TestResolveAgentConfigKeepsASavedAccountsRefusalTheComputersCatalogLists(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(dir, "device"))
	saved := filepath.Join(dir, "saved")
	if err := codexagent.DefaultUnavailableModels().Record(saved, "gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	catalog := &refreshCountingCatalog{catalog: ports.AgentModelCatalog{
		ValidatedAt: time.Now().Add(time.Minute),
		Models:      []ports.AgentModelInfo{{ID: "gpt-6.1-sol"}, {ID: "gpt-5.2-codex"}},
	}}
	m := &Manager{
		backgroundContext: context.Background(), modelCatalog: catalog,
		agentAccounts: codexAccountsFake{homes: map[string]string{"": "", "saved": saved}},
	}
	for range 2 {
		_, err := codexSpawn(m, "saved", "gpt-5.2-codex", domain.ProjectConfig{})
		if !errors.Is(err, ErrUnsupportedModel) {
			t.Fatalf("saved account's refused model: error = %v, want the refusal kept", err)
		}
		if strings.Contains(err.Error(), "gpt-6.1-sol") || !strings.Contains(err.Error(), "choose another model") {
			t.Fatalf("error = %q, want the plain refusal without the computer's models", err)
		}
	}
	if !codexagent.DefaultUnavailableModels().Unavailable(saved, "gpt-5.2-codex") {
		t.Fatal("the computer's catalog must not forget a saved account's refusal")
	}
	if got := catalog.refreshCount(); got != 0 {
		t.Fatalf("rediscoveries for a saved account's refusal = %d, want 0", got)
	}
}

// A saved account or project-specific CODEX_HOME has no matching model catalog.
// Its refusal blocks new sessions for a day, then permits one retry so a plan
// change or model rollout cannot leave that account blocked forever.
func TestResolveAgentConfigRetriesOldRefusalsWithoutAnAccountCatalog(t *testing.T) {
	for _, tc := range []struct {
		name        string
		accountID   string
		projectHome bool
	}{
		{name: "saved account", accountID: "saved"},
		{name: "project CODEX_HOME", projectHome: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codexagent.ConfigureUnavailableModels("")
			t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
			dir := t.TempDir()
			t.Setenv("CODEX_HOME", filepath.Join(dir, "device"))
			home := filepath.Join(dir, "other-account")
			project := domain.ProjectConfig{}
			if tc.projectHome {
				project.Env = map[string]string{"CODEX_HOME": home}
			}
			store := codexagent.DefaultUnavailableModels()
			model := "gpt-5.2-codex"
			if err := store.Record(home, model, time.Now().Add(-25*time.Hour)); err != nil {
				t.Fatal(err)
			}
			catalog := &refreshCountingCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: model}}}}
			m := &Manager{
				backgroundContext: context.Background(), modelCatalog: catalog,
				agentAccounts: codexAccountsFake{homes: map[string]string{"": "", "saved": home}},
			}
			if _, err := codexSpawn(m, tc.accountID, model, project); err != nil {
				t.Fatalf("spawn after the retry window: %v", err)
			}
			if store.Unavailable(home, model) {
				t.Fatal("the old refusal was not forgotten before retrying")
			}
			if err := store.Record(home, model, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := codexSpawn(m, tc.accountID, model, project); !errors.Is(err, ErrUnsupportedModel) {
				t.Fatalf("spawn after a fresh refusal: error = %v, want ErrUnsupportedModel", err)
			}
			if got := catalog.refreshCount(); got != 0 {
				t.Fatalf("another account's catalog was refreshed %d times, want 0", got)
			}
		})
	}
}
