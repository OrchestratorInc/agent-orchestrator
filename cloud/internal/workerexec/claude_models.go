package workerexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	acp "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// DiscoverClaudeModels asks the installed Claude ACP adapter for its live
// session options. These are scoped to the worker's credentials and provider.
func DiscoverClaudeModels(ctx context.Context, command Command, nativeConversationID, selectedModel string) (worker.ChatModelsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if command.Path == "" {
		return worker.ChatModelsResponse{}, errors.New("Claude executable is unavailable")
	}
	adapter, err := EnsureClaudeACPAdapter(ctx)
	if err != nil {
		return worker.ChatModelsResponse{}, err
	}
	if command.Path == claudeCodeBinary {
		// List models with the Claude Code that will run them.
		command.Path = EnsureClaudeCode(ctx)
	}
	process := exec.CommandContext(ctx, adapter)
	configureProviderProcess(process)
	process.Dir = command.Dir
	provider := discoverClaudeProviderModels(ctx, command)
	env := claudeModelEnvironment(command, selectedModel, provider)
	process.Env = mergedEnvironment(env)
	process.Stderr = io.Discard
	stdin, err := process.StdinPipe()
	if err != nil {
		return worker.ChatModelsResponse{}, err
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return worker.ChatModelsResponse{}, err
	}
	if err := process.Start(); err != nil {
		return worker.ChatModelsResponse{}, err
	}
	defer func() { _ = stopProviderProcess(process); _ = process.Wait() }()
	conn := acp.NewClientSideConnection(&cloudACPClient{}, stdin, stdout)
	if _, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		return worker.ChatModelsResponse{}, fmt.Errorf("initialize Claude ACP: %w", err)
	}
	if nativeConversationID != "" {
		loaded, loadErr := conn.LoadSession(ctx, acp.LoadSessionRequest{
			Cwd: command.Dir, McpServers: []acp.McpServer{}, SessionId: acp.SessionId(nativeConversationID),
		})
		if loadErr == nil {
			models, model, effort := claudeModelsFromOptions(loaded.ConfigOptions)
			if len(models) > 0 {
				probeClaudeApprovalModes(ctx, conn, acp.SessionId(nativeConversationID), models, model, loaded.ConfigOptions, loaded.Modes)
				return claudeModelCatalog(models, model, effort, provider, loaded.ConfigOptions, loaded.Modes), nil
			}
		}
		// A stale native identity should not hide the model picker. The next
		// real turn makes the same recovery decision in the Chat controller.
	}
	session, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: command.Dir, McpServers: []acp.McpServer{}})
	if err != nil {
		return worker.ChatModelsResponse{}, fmt.Errorf("create Claude ACP model session: %w", err)
	}
	models, model, effort := claudeModelsFromOptions(session.ConfigOptions)
	if len(models) == 0 {
		return worker.ChatModelsResponse{}, errors.New("Claude ACP did not advertise model choices")
	}
	probeClaudeApprovalModes(ctx, conn, session.SessionId, models, model, session.ConfigOptions, session.Modes)
	return claudeModelCatalog(models, model, effort, provider, session.ConfigOptions, session.Modes), nil
}

// probeClaudeApprovalModes fills in each model's approval modes on the
// discovery session (claudeApprovalModesByModel) by switching the session to
// the model and reading the options Claude ACP then advertises. The probes
// share one short budget so a slow adapter cannot hold up the catalog, and the
// session is put back on its own model afterwards. No turn runs on this
// session: it belongs to the discovery process, which ends with the request.
func probeClaudeApprovalModes(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionId, models []worker.ChatModel, current string, options []acp.SessionConfigOption, modes *acp.SessionModeState) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	setModel := func(model string) ([]acp.SessionConfigOption, error) {
		response, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sessionID, ConfigId: "model", Value: acp.SessionConfigValueId(model),
		}})
		if err != nil {
			return nil, err
		}
		return response.ConfigOptions, nil
	}
	switched := false
	claudeApprovalModesByModel(models, current, options, modes, func(model string) ([]acp.SessionConfigOption, error) {
		switched = true
		return setModel(model)
	})
	if switched && current != "" {
		_, _ = setModel(current)
	}
}

// claudeApprovalModesByModel sets each model's ApprovalModes to the approval
// modes Claude ACP offers for it. The session's own options and modes describe
// its current model; any other model is probed, since the adapter recomputes
// its modes on a model change (auto only for a model that supports it).
// Without a mode option a switch reports no modes, so other models are left
// unknown rather than probed. A model whose probe fails keeps no modes, which
// a client reads as unknown.
func claudeApprovalModesByModel(models []worker.ChatModel, current string, options []acp.SessionConfigOption, modes *acp.SessionModeState, probe func(model string) ([]acp.SessionConfigOption, error)) {
	modeOption := false
	for _, option := range options {
		if option.Select != nil && option.Select.Id == "mode" {
			modeOption = true
		}
	}
	for index := range models {
		if models[index].ID == current {
			models[index].ApprovalModes = claudeApprovalModesFromOptions(options, modes)
			continue
		}
		if !modeOption {
			continue
		}
		probed, err := probe(models[index].ID)
		if err != nil {
			continue
		}
		models[index].ApprovalModes = claudeApprovalModesFromOptions(probed, nil)
	}
}

// claudeApprovalModesFromOptions lists AO's approval modes the session offers,
// in the Chat menu's order.
func claudeApprovalModesFromOptions(options []acp.SessionConfigOption, modes *acp.SessionModeState) []string {
	var result []string
	for _, mode := range []string{"default", "acceptEdits", "auto", "bypassPermissions"} {
		if acpModeOffered(options, modes, mode) {
			result = append(result, claudeACPModeApproval(mode))
		}
	}
	return result
}

func claudeModelsFromOptions(options []acp.SessionConfigOption) ([]worker.ChatModel, string, string) {
	var models []worker.ChatModel
	var currentModel, currentEffort string
	var efforts []string
	for _, option := range options {
		selectOption := option.Select
		if selectOption == nil {
			continue
		}
		id := string(selectOption.Id)
		if id != "model" && id != "effort" {
			continue
		}
		choices := make([]acp.SessionConfigSelectOption, 0)
		if selectOption.Options.Ungrouped != nil {
			choices = append(choices, *selectOption.Options.Ungrouped...)
		}
		if selectOption.Options.Grouped != nil {
			for _, group := range *selectOption.Options.Grouped {
				choices = append(choices, group.Options...)
			}
		}
		if id == "effort" {
			currentEffort = string(selectOption.CurrentValue)
			for _, choice := range choices {
				if value := strings.TrimSpace(string(choice.Value)); value != "" {
					efforts = append(efforts, value)
				}
			}
			continue
		}
		currentModel = string(selectOption.CurrentValue)
		for _, choice := range choices {
			value := strings.TrimSpace(string(choice.Value))
			if value == "" {
				continue
			}
			name := strings.TrimSpace(choice.Name)
			if name == "" {
				name = value
			}
			if value == "default" {
				// Claude ACP describes which concrete model its implicit choice resolves to.
				name = "Use agent model"
				if choice.Description != nil && strings.TrimSpace(*choice.Description) != "" {
					name = strings.TrimSpace(*choice.Description)
				}
			}
			model := worker.ChatModel{ID: value, DisplayName: name, Default: value == currentModel}
			if choice.Description != nil {
				model.Description = *choice.Description
			}
			models = append(models, model)
		}
	}
	// ACP may change its effort choices after a model change. The current list
	// belongs only to the selected model; do not advertise it on other models.
	for index := range models {
		if models[index].ID == currentModel {
			models[index].Efforts = append([]string(nil), efforts...)
			models[index].DefaultEffort = currentEffort
		}
	}
	return models, currentModel, currentEffort
}

// The same non-billable provider probe used by local AO. Inconclusive discovery
// leaves the adapter's own catalog available; it never blocks a working login.
func discoverClaudeProviderModels(ctx context.Context, command Command) []agentcreds.Model {
	lookup := func(key string) string {
		if value, ok := command.Env[key]; ok {
			return value
		}
		return os.Getenv(key)
	}
	var config map[string]json.RawMessage
	if raw := lookup("CLAUDE_MODEL_CONFIG"); raw != "" {
		if json.Unmarshal([]byte(raw), &config) != nil || config == nil {
			return nil
		}
		if _, explicit := config["availableModels"]; explicit {
			return nil
		}
	}
	opts := agentcreds.ResolveOptions{Env: lookup, WorkingDir: command.Dir, CommandEnv: command.Env}.WithClaudeSettings(ctx)
	result := agentcreds.New(nil).ValidateLocal(ctx, "", opts)
	return result.Models
}

func claudeModelEnvironment(command Command, selected string, models []agentcreds.Model) map[string]string {
	env := make(map[string]string, len(command.Env)+3)
	for key, value := range command.Env {
		env[key] = value
	}
	env["CLAUDE_CODE_EXECUTABLE"] = command.Path
	native := selected == "" || selected == "default" || selected == "sonnet" || selected == "opus" || selected == "haiku" || selected == "fable" || selected == "opus[1m]"
	if !native && env["ANTHROPIC_CUSTOM_MODEL_OPTION"] == "" && os.Getenv("ANTHROPIC_CUSTOM_MODEL_OPTION") == "" {
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = selected
	}
	raw, ok := env["CLAUDE_MODEL_CONFIG"]
	if !ok {
		raw = os.Getenv("CLAUDE_MODEL_CONFIG")
	}
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		if json.Unmarshal([]byte(raw), &config) != nil || config == nil {
			return env
		}
		if _, explicit := config["availableModels"]; explicit {
			return env
		}
	}
	var ids []string
	seen := map[string]bool{}
	for _, model := range models {
		if id := strings.TrimSpace(model.ID); id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if selected != "" && !seen[selected] && (!native || len(ids) > 0) {
		ids = append(ids, selected)
	}
	if len(ids) == 0 {
		return env
	}
	config["availableModels"], _ = json.Marshal(ids)
	encoded, _ := json.Marshal(config)
	env["CLAUDE_MODEL_CONFIG"] = string(encoded)
	return env
}

func claudeModesFromOptions(options []acp.SessionConfigOption, modes *acp.SessionModeState) []string {
	var result []string
	for _, id := range []string{"default", "plan"} {
		if acpModeOffered(options, modes, id) {
			result = append(result, id)
		}
	}
	return result
}

func claudeModelCatalog(models []worker.ChatModel, model, effort string, provider []agentcreds.Model, options []acp.SessionConfigOption, modes *acp.SessionModeState) worker.ChatModelsResponse {
	for i := range models {
		if offered, ok := providerModelFor(models[i], provider); ok {
			if offered.ID == models[i].ID && offered.DisplayName != "" {
				models[i].DisplayName = offered.DisplayName
			}
			models[i].Efforts = append([]string(nil), offered.Efforts...)
		}
	}
	return worker.ChatModelsResponse{Models: models, Model: model, ReasoningEffort: effort, Modes: claudeModesFromOptions(options, modes)}
}

// providerModelFor finds the provider model behind a Claude ACP choice. An
// alias such as "opus" has no provider ID of its own, but Claude ACP describes
// what it resolves to ("Opus 5.5 · Best for everyday, complex tasks"), and the
// provider names that model "Claude Opus 5.5". Without this match an alias,
// the usual pick, would carry no effort levels and Chat would hide the effort
// control whenever one is selected.
func providerModelFor(model worker.ChatModel, provider []agentcreds.Model) (agentcreds.Model, bool) {
	for _, offered := range provider {
		if offered.ID == model.ID {
			return offered, true
		}
	}
	resolved, _, _ := strings.Cut(model.Description, "·")
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return agentcreds.Model{}, false
	}
	for _, offered := range provider {
		name := strings.TrimSpace(offered.DisplayName)
		if strings.EqualFold(name, resolved) || strings.EqualFold(name, "Claude "+resolved) {
			return offered, true
		}
	}
	return agentcreds.Model{}, false
}
