package workerexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	acp "github.com/coder/acp-go-sdk"
)

// DiscoverClaudeModels asks the installed Claude ACP adapter for its live
// session options. These are scoped to the worker's credentials and provider.
func DiscoverClaudeModels(ctx context.Context, command Command, nativeConversationID string) ([]worker.ChatModel, string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if command.Path == "" {
		return nil, "", "", errors.New("Claude executable is unavailable")
	}
	process := exec.CommandContext(ctx, "claude-agent-acp")
	configureProviderProcess(process)
	process.Dir = command.Dir
	env := make(map[string]string, len(command.Env)+1)
	for key, value := range command.Env {
		env[key] = value
	}
	env["CLAUDE_CODE_EXECUTABLE"] = command.Path
	process.Env = mergedEnvironment(env)
	process.Stderr = io.Discard
	stdin, err := process.StdinPipe()
	if err != nil {
		return nil, "", "", err
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return nil, "", "", err
	}
	if err := process.Start(); err != nil {
		return nil, "", "", err
	}
	defer func() { _ = stopProviderProcess(process); _ = process.Wait() }()
	conn := acp.NewClientSideConnection(&cloudACPClient{}, stdin, stdout)
	if _, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		return nil, "", "", fmt.Errorf("initialize Claude ACP: %w", err)
	}
	if nativeConversationID != "" {
		loaded, loadErr := conn.LoadSession(ctx, acp.LoadSessionRequest{
			Cwd: command.Dir, McpServers: []acp.McpServer{}, SessionId: acp.SessionId(nativeConversationID),
		})
		if loadErr == nil {
			models, model, effort := claudeModelsFromOptions(loaded.ConfigOptions)
			if len(models) > 0 {
				return models, model, effort, nil
			}
		}
		// A stale native identity should not hide the model picker. The next
		// real turn makes the same recovery decision in the Chat controller.
	}
	session, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: command.Dir, McpServers: []acp.McpServer{}})
	if err != nil {
		return nil, "", "", fmt.Errorf("create Claude ACP model session: %w", err)
	}
	models, model, effort := claudeModelsFromOptions(session.ConfigOptions)
	if len(models) == 0 {
		return nil, "", "", errors.New("Claude ACP did not advertise model choices")
	}
	return models, model, effort, nil
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
