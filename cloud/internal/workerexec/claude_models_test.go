package workerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// The catalog reports, per model, the approval modes Claude ACP offers for it:
// the session's own model from its options, every other model from the
// options the adapter advertises once the discovery session switches to it.
// Claude ACP offers auto only where the model supports it, so Haiku lists no
// auto. The session is put back on its own model afterwards.
func TestClaudeCatalogReportsApprovalModesPerModelFromACPOptions(t *testing.T) {
	agent := &claudeModelAgent{modeSpyAgent: modeSpyAgent{selected: make(chan acp.SessionModeId, 1)}}
	conn := claudeTestConnection(t, agent)
	options := claudeTestOptions("opus")
	models, current, _ := claudeModelsFromOptions(options)
	saved := claudeProbes
	claudeProbes = &claudeProbeCache{}
	t.Cleanup(func() { claudeProbes = saved })
	probeClaudeApprovalModes(context.Background(), conn, "adapter", "session-1", models, current, options, nil)
	byID := map[string][]string{}
	for _, model := range models {
		byID[model.ID] = model.ApprovalModes
	}
	if got, want := byID["opus"], []string{"default", "accept-edits", "auto", "bypass-permissions"}; !slices.Equal(got, want) {
		t.Fatalf("opus approval modes = %v, want %v", got, want)
	}
	if got, want := byID["haiku"], []string{"default", "accept-edits", "bypass-permissions"}; !slices.Equal(got, want) {
		t.Fatalf("haiku approval modes = %v, want %v", got, want)
	}
	if got, want := agent.models(), []string{"haiku", "opus"}; !slices.Equal(got, want) {
		t.Fatalf("model switches = %v, want a probe of haiku and a return to opus", got)
	}

	// The next catalog request on this worker reuses the probe: no model is
	// switched, so the catalog costs what it did before probing existed.
	again, _, _ := claudeModelsFromOptions(options)
	probeClaudeApprovalModes(context.Background(), conn, "adapter", "session-1", again, current, options, nil)
	if got, want := agent.models(), []string{"haiku", "opus"}; !slices.Equal(got, want) {
		t.Fatalf("model switches after a repeat request = %v, want none beyond %v", got, want)
	}
	for _, model := range again {
		if model.ID == "haiku" && !slices.Equal(model.ApprovalModes, []string{"default", "accept-edits", "bypass-permissions"}) {
			t.Fatalf("cached haiku approval modes = %v", model.ApprovalModes)
		}
	}
	// Another adapter (an upgraded Claude Code) is probed afresh.
	other, _, _ := claudeModelsFromOptions(options)
	probeClaudeApprovalModes(context.Background(), conn, "upgraded adapter", "session-1", other, current, options, nil)
	if got := len(agent.models()); got != 4 {
		t.Fatalf("model switches for a new adapter = %v, want a fresh probe", agent.models())
	}

	// A model whose probe fails, and every other model of a session without a
	// mode option, are unknown rather than guessed: the client keeps offering
	// what the session allows.
	failing := []worker.ChatModel{{ID: "opus"}, {ID: "haiku"}}
	claudeApprovalModesByModel(failing, "opus", options, nil, func(string) ([]string, error) {
		return nil, fmt.Errorf("adapter went away")
	})
	if failing[0].ApprovalModes == nil || failing[1].ApprovalModes != nil {
		t.Fatalf("failed probe = %+v, want opus known and haiku unknown", failing)
	}
	modes := &acp.SessionModeState{AvailableModes: []acp.SessionMode{{Id: "default"}, {Id: "acceptEdits"}}}
	unprobed := []worker.ChatModel{{ID: "opus"}, {ID: "haiku"}}
	claudeApprovalModesByModel(unprobed, "opus", nil, modes, func(model string) ([]string, error) {
		t.Fatalf("probed %s without a mode option", model)
		return nil, nil
	})
	if !slices.Equal(unprobed[0].ApprovalModes, []string{"default", "accept-edits"}) || unprobed[1].ApprovalModes != nil {
		t.Fatalf("modes without a mode option = %+v", unprobed)
	}
}

// An older desktop ignores the field, and a catalog from an older worker has
// none: approvalModes is omitted when unknown.
func TestChatModelApprovalModesAreOptionalOnTheWire(t *testing.T) {
	raw, err := json.Marshal(worker.ChatModel{ID: "opus", DisplayName: "Opus"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "approvalModes") {
		t.Fatalf("unknown approval modes were sent: %s", raw)
	}
	var model worker.ChatModel
	if err := json.Unmarshal([]byte(`{"id":"haiku","displayName":"Haiku","default":false,"approvalModes":["default","accept-edits"]}`), &model); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(model.ApprovalModes, []string{"default", "accept-edits"}) {
		t.Fatalf("approval modes = %v", model.ApprovalModes)
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

// A model's approval modes follow from the Claude Code build and the account
// and provider it runs with, so the probe cache key changes with the adapter
// environment and with an in-place upgrade of either executable, and an
// entry lapses after claudeProbeTTL. Only a probe that really switched the
// session is remembered.
func TestClaudeProbeCacheKeyAndLifetime(t *testing.T) {
	dir := t.TempDir()
	adapter := filepath.Join(dir, "claude-agent-acp")
	claude := filepath.Join(dir, "claude")
	for _, path := range []string{adapter, claude} {
		if err := os.WriteFile(path, []byte("v1"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "one"}
	base := claudeProbeKey(adapter, claude, env)
	if again := claudeProbeKey(adapter, claude, map[string]string{"ANTHROPIC_API_KEY": "one"}); again != base {
		t.Fatal("the same adapter, Claude Code and environment gave a different key")
	}
	if strings.Contains(base, "one") {
		t.Fatal("the key carries a credential in clear")
	}
	if claudeProbeKey(adapter, claude, map[string]string{"ANTHROPIC_API_KEY": "two"}) == base {
		t.Fatal("another credential kept the key")
	}
	if claudeProbeKey(adapter, claude, map[string]string{"ANTHROPIC_API_KEY": "one", "CLAUDE_CODE_USE_BEDROCK": "1"}) == base {
		t.Fatal("another provider kept the key")
	}
	if err := os.WriteFile(claude, []byte("v2, upgraded in place"), 0o755); err != nil {
		t.Fatal(err)
	}
	if claudeProbeKey(adapter, claude, env) == base {
		t.Fatal("an in-place Claude Code upgrade kept the key")
	}

	cache := &claudeProbeCache{}
	start := time.Now()
	cache.put("key", "haiku", []string{"default", "accept-edits"}, start)
	if modes, ok := cache.get("key", "haiku", start.Add(claudeProbeTTL-time.Second)); !ok || !slices.Equal(modes, []string{"default", "accept-edits"}) {
		t.Fatalf("fresh entry = %v %v", modes, ok)
	}
	if _, ok := cache.get("other key", "haiku", start); ok {
		t.Fatal("an entry answered for another key")
	}
	if _, ok := cache.get("key", "haiku", start.Add(claudeProbeTTL)); ok {
		t.Fatal("an entry outlived claudeProbeTTL")
	}
}

// Modes read after a switch the adapter did not make describe the old model,
// so they are neither reported nor remembered for the probed one.
func TestClaudeProbeIgnoresASwitchThatDidNotHappen(t *testing.T) {
	saved := claudeProbes
	claudeProbes = &claudeProbeCache{}
	t.Cleanup(func() { claudeProbes = saved })
	agent := &claudeModelAgent{modeSpyAgent: modeSpyAgent{selected: make(chan acp.SessionModeId, 1)}, stuck: "opus"}
	conn := claudeTestConnection(t, agent)
	options := claudeTestOptions("opus")
	models, current, _ := claudeModelsFromOptions(options)
	probeClaudeApprovalModes(context.Background(), conn, "adapter", "session-1", models, current, options, nil)
	for _, model := range models {
		if model.ID == "haiku" && model.ApprovalModes != nil {
			t.Fatalf("haiku modes = %v from a switch that stayed on opus, want unknown", model.ApprovalModes)
		}
	}
	if _, ok := claudeProbes.get("adapter", "haiku", time.Now()); ok {
		t.Fatal("a switch that did not happen was remembered")
	}
}
