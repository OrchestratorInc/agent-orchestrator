package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A Codex spawn on a model Codex already refused for the ChatGPT account fails
// at spawn and names the models the account lists, instead of starting a
// worker whose first turn fails (#4518). A model Codex never refused is still
// left to the CLI, as #8119 decided.
func TestResolveAgentConfigRefusesACodexModelTheAccountRefused(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })
	if err := codexagent.DefaultUnavailableModels().Record("gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	account := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gpt-6.1-sol", Efforts: []string{"low", "high"}},
		{ID: "gpt-5.6-sol", Efforts: []string{"low", "high"}},
	}}
	m := &Manager{backgroundContext: context.Background(), modelCatalog: tuningCatalog{catalog: account}}
	spawn := func(model, effort string) (ports.AgentConfig, error) {
		return m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
			AgentConfig: ports.AgentConfig{Model: model, Effort: effort}, EffortOverride: effort != "",
		}, domain.ProjectConfig{})
	}

	for _, effort := range []string{"", "high"} {
		_, err := spawn("gpt-5.2-codex", effort)
		if !errors.Is(err, ErrUnsupportedModel) {
			t.Fatalf("refused model (effort %q) error = %v, want ErrUnsupportedModel", effort, err)
		}
		if !strings.Contains(err.Error(), "gpt-6.1-sol") || !strings.Contains(err.Error(), "gpt-5.6-sol") {
			t.Fatalf("error = %q, want the models the account lists", err)
		}
	}
	if _, err := spawn("gpt-custom", ""); err != nil {
		t.Fatalf("unlisted model Codex never refused: error = %v, want the launch left to the CLI", err)
	}
	if _, err := spawn("", ""); err != nil {
		t.Fatalf("default model: error = %v, want launch", err)
	}

	// The account lists the model again (another sign-in): allow and forget.
	account.Models = append(account.Models, ports.AgentModelInfo{ID: "gpt-5.2-codex"})
	m.modelCatalog = tuningCatalog{catalog: account}
	if _, err := spawn("gpt-5.2-codex", ""); err != nil {
		t.Fatalf("model the account lists again: error = %v, want launch", err)
	}
	if codexagent.DefaultUnavailableModels().Unavailable("gpt-5.2-codex") {
		t.Fatal("a model the account lists again must be forgotten")
	}
}
