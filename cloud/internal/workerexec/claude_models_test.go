package workerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestClaudeModelsFromACPOptions(t *testing.T) {
	modelChoices := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "sonnet", Name: "Claude Sonnet"},
		{Value: "opus", Name: "Claude Opus"},
	}
	effortChoices := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "medium", Name: "Medium"},
		{Value: "high", Name: "High"},
	}
	models, current, effort := claudeModelsFromOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{Id: "model", CurrentValue: "sonnet", Options: acp.SessionConfigSelectOptions{Ungrouped: &modelChoices}}},
		{Select: &acp.SessionConfigOptionSelect{Id: "effort", CurrentValue: "high", Options: acp.SessionConfigSelectOptions{Ungrouped: &effortChoices}}},
	})
	if current != "sonnet" || effort != "high" || len(models) != 2 || !models[0].Default || len(models[0].Efforts) != 2 || len(models[1].Efforts) != 0 {
		t.Fatalf("catalog = %+v, current = %q/%q", models, current, effort)
	}
}

func TestClaudeDefaultModelUsesProviderResolvedName(t *testing.T) {
	resolved := "Claude Opus"
	choices := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "default", Name: "Default (recommended)", Description: &resolved},
		{Value: "opus", Name: "Claude Opus"},
	}
	models, current, _ := claudeModelsFromOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{Id: "model", CurrentValue: "default", Options: acp.SessionConfigSelectOptions{Ungrouped: &choices}}},
	})
	if current != "default" || models[0].DisplayName != "Claude Opus" {
		t.Fatalf("catalog = %+v, current = %q", models, current)
	}
}

func TestCloudClaudeProviderModelsReachAdapter(t *testing.T) {
	models := []agentcreds.Model{{ID: "claude-opus-5", DisplayName: "Opus 5", Efforts: []string{"low", "high"}}, {ID: "claude-sonnet-5", DisplayName: "Sonnet 5"}}
	command := Command{Path: "/bin/claude", Env: map[string]string{"CLAUDE_MODEL_CONFIG": `{"other":true}`}}
	env := claudeModelEnvironment(command, "claude-opus-5", models)
	var config struct {
		AvailableModels []string `json:"availableModels"`
		Other           bool     `json:"other"`
	}
	if err := json.Unmarshal([]byte(env["CLAUDE_MODEL_CONFIG"]), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.AvailableModels) != 2 || config.AvailableModels[0] != "claude-opus-5" || !config.Other {
		t.Fatalf("provider catalog missing: %+v", config)
	}
	if env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "claude-opus-5" {
		t.Fatal("concrete selected model is not selectable by SDK")
	}
	if command.Env["CLAUDE_MODEL_CONFIG"] != `{"other":true}` {
		t.Fatal("mutated launch environment")
	}
	env = claudeModelEnvironment(command, "opus", models)
	if err := json.Unmarshal([]byte(env["CLAUDE_MODEL_CONFIG"]), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.AvailableModels) != 3 || config.AvailableModels[2] != "opus" {
		t.Fatalf("lost model selected in Terminal: %+v", config)
	}
	command.Env["CLAUDE_MODEL_CONFIG"] = `{"availableModels":["private-model"]}`
	if got := claudeModelEnvironment(command, "opus", models)["CLAUDE_MODEL_CONFIG"]; got != command.Env["CLAUDE_MODEL_CONFIG"] {
		t.Fatalf("overrode explicit model restriction: %s", got)
	}
}

func TestCloudClaudeModesComeFromAdapter(t *testing.T) {
	modes := &acp.SessionModeState{CurrentModeId: "default", AvailableModes: []acp.SessionMode{{Id: "default", Name: "Agent"}, {Id: "plan", Name: "Plan"}}}
	got := claudeModesFromOptions(nil, modes)
	if len(got) != 2 || got[1] != "plan" {
		t.Fatalf("lost native Plan capability: %v", got)
	}
}

func TestCloudClaudeDiscoversProviderCatalogWithLaunchCredentials(t *testing.T) {
	var requested bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "fixture-key" {
			t.Errorf("wrong provider probe: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"claude-opus-5","display_name":"Claude Opus 5","capabilities":{"effort":{"supported":true,"low":{"supported":true},"high":{"supported":true}}}}]}`)
	}))
	defer server.Close()
	command := Command{Dir: t.TempDir(), Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "fixture-key", "CLAUDE_CODE_OAUTH_TOKEN": "", "ANTHROPIC_AUTH_TOKEN": "", "CLAUDE_CONFIG_DIR": t.TempDir(), "CLAUDE_CODE_USE_BEDROCK": "", "CLAUDE_CODE_USE_VERTEX": "", "CLAUDE_CODE_USE_FOUNDRY": ""}}
	got := discoverClaudeProviderModels(context.Background(), command)
	if !requested || len(got) != 1 || got[0].ID != "claude-opus-5" || len(got[0].Efforts) != 2 {
		t.Fatalf("provider catalog lost: %+v", got)
	}
	requested = false
	command.Env["CLAUDE_MODEL_CONFIG"] = `{"availableModels":["company-model"]}`
	if got := discoverClaudeProviderModels(context.Background(), command); len(got) != 0 || requested {
		t.Fatalf("explicit catalog was queried or replaced: %+v", got)
	}
}

// Claude ACP lists aliases ("opus") ahead of concrete IDs, and an alias has no
// provider ID of its own. It must still carry the effort levels of the model it
// resolves to, or Chat hides the effort control whenever an alias is selected.
func TestClaudeModelCatalogGivesAliasesTheirModelsEfforts(t *testing.T) {
	five := []string{"low", "medium", "high", "xhigh", "max"}
	models := []worker.ChatModel{
		{ID: "opus", DisplayName: "Opus", Description: "Opus 5.5 · Best for everyday, complex tasks"},
		{ID: "haiku", DisplayName: "Haiku", Description: "Haiku 4.5 · Fastest for quick answers"},
		{ID: "claude-opus-5", DisplayName: "claude-opus-5", Description: "Newer version available · select Opus for Opus 5.5"},
		{ID: "default", DisplayName: "Use agent model"},
	}
	provider := []agentcreds.Model{
		{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5", Efforts: five},
		{ID: "claude-opus-5", DisplayName: "Claude Opus 5", Efforts: five},
		{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5"},
	}
	catalog := claudeModelCatalog(models, "opus", "", provider, nil, nil)
	byID := map[string]worker.ChatModel{}
	for _, model := range catalog.Models {
		byID[model.ID] = model
	}
	if got := byID["opus"]; got.DisplayName != "Opus" || len(got.Efforts) != 5 {
		t.Fatalf("opus = %+v, want its alias label with Opus 5.5's five efforts", got)
	}
	if got := byID["haiku"].Efforts; len(got) != 0 {
		t.Fatalf("haiku efforts = %v, want none (Haiku 4.5 takes no effort)", got)
	}
	if got := byID["claude-opus-5"]; got.DisplayName != "Claude Opus 5" || len(got.Efforts) != 5 {
		t.Fatalf("claude-opus-5 = %+v, want the provider's label and efforts", got)
	}
	if got := byID["default"].Efforts; len(got) != 0 {
		t.Fatalf("default efforts = %v, want none without a resolvable description", got)
	}
}
