package deepseekharnessacp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const acpProfile = "acp"

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessDeepSeek,
		Configure:            configure,
		SessionOptions:       sessionOptions,
		PromptFailureCode:    promptFailureCode,
		SessionPreamble:      SessionPreamble,
		PermissionPolicy:     permissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func promptFailureCode(err error) domain.ConversationFailureCode {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 {
		return ""
	}
	detail, _ := requestErr.Data.(string)
	if detail == "" && strings.HasPrefix(requestErr.Message, "Internal error: ") {
		detail = strings.TrimPrefix(requestErr.Message, "Internal error: ")
	}
	if !strings.HasPrefix(strings.TrimSpace(detail),
		`turn failed: llm-deepseek: no API key for provider route "deepseek-official";`) {
		return ""
	}
	return domain.ConversationFailureProviderKeyMissing
}

func configure(_ context.Context, _ acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	return []string{"--profile", acpProfile}, nil, nil
}

var deepseekEfforts = map[string]string{
	"none":    "off",
	"minimal": "off",
	"off":     "off",
	"low":     "low",
	"medium":  "high",
	"high":    "high",
	"xhigh":   "max",
	"max":     "max",
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := make([]acpdriver.SessionOption, 0, 2)
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "reasoning_effort", Value: deepseekEffort(effort)})
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

func SessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(
		"Standing instructions from Agent Orchestrator. Follow them for this whole session:\n" +
			"<ao_standing_instructions>\n" + standing + "\n</ao_standing_instructions>",
	)}
}

func deepseekEffort(effort string) string {
	if mapped, ok := deepseekEfforts[strings.ToLower(effort)]; ok {
		return mapped
	}
	return effort
}

func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	effort := strings.TrimSpace(settings.Effort)
	if effort == "" {
		return nil
	}
	switch deepseekEffort(effort) {
	case "off", "low", "high", "max":
		return nil
	default:
		return fmt.Errorf("%w: DeepSeek Harness reasoning effort %q is not one of off, low, high, max", ports.ErrChatConfigOptionInvalid, settings.Effort)
	}
}

func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	mode = ports.NormalizePermissionMode(mode)
	kind := params.ToolCall.Kind
	if mode != ports.PermissionModeAuto && (mode != ports.PermissionModeAcceptEdits || kind == nil ||
		(*kind != acpsdk.ToolKindEdit && *kind != acpsdk.ToolKindDelete && *kind != acpsdk.ToolKindMove)) {
		return "", false
	}
	for _, option := range params.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}
