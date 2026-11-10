package acp

import (
	"context"
	"slices"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// discoverSessionModels 只采纳切换后对应模型的完整配置；单模型探测失败不抹掉整个目录。
func discoverSessionModels(ctx context.Context, conn *acpsdk.ClientSideConnection, initial acpsdk.NewSessionResponse) ([]ports.AgentModelInfo, error) {
	options := normalizeConfigOptions(initial.ConfigOptions)
	var models []ports.AgentModelInfo
	for _, option := range options {
		if option.Type != ports.ChatConfigOptionSelect || (option.Category != "model" && option.ID != "model") {
			continue
		}
		for _, choice := range option.Choices {
			id := strings.TrimSpace(choice.Value)
			if id == "" {
				continue
			}
			label := strings.TrimSpace(choice.Name)
			if label == "" {
				label = id
			}
			provider := strings.TrimSpace(choice.Group)
			if provider == "" {
				if prefix, _, ok := strings.Cut(id, "/"); ok {
					provider = prefix
				}
			}
			model := ports.AgentModelInfo{ID: id, Label: label, Provider: provider, IsDefault: id == option.Current.Select}
			modelOptions := options
			if !model.IsDefault {
				resp, err := conn.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
					ValueId: &acpsdk.SetSessionConfigOptionValueId{
						SessionId: initial.SessionId, ConfigId: acpsdk.SessionConfigId(option.ID), Value: acpsdk.SessionConfigValueId(id),
					},
				})
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				modelOptions = nil
				if err == nil {
					modelOptions = normalizeConfigOptions(resp.ConfigOptions)
				}
			}
			model.Efforts, model.DefaultEffort = modelReasoningOptions(modelOptions, option.ID, id)
			models = append(models, model)
		}
	}
	return models, nil
}

// modelReasoningOptions 要求返回目录确认当前模型，防止失败或旧响应污染其他模型的档位。
func modelReasoningOptions(options []ports.ChatConfigOption, modelOptionID, modelID string) ([]string, string) {
	selected := false
	for _, option := range options {
		if option.ID == modelOptionID && option.Current.Select == modelID {
			selected = true
			break
		}
	}
	if !selected {
		return nil, ""
	}
	for _, option := range options {
		if option.Type != ports.ChatConfigOptionSelect ||
			(option.Category != "thought_level" && option.ID != "reasoning_effort") {
			continue
		}
		var efforts []string
		for _, choice := range option.Choices {
			if choice.Value != "" && !strings.EqualFold(choice.Value, "default") && !slices.Contains(efforts, choice.Value) {
				efforts = append(efforts, choice.Value)
			}
		}
		if slices.Contains(efforts, option.Current.Select) {
			return efforts, option.Current.Select
		}
		return efforts, ""
	}
	return nil, ""
}
