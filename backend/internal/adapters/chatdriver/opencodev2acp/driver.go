package opencodev2acp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/opencodeidentity"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessOpenCodeV2,
		Configure:            configure,
		SessionMode:          sessionMode,
		SessionOptions:       sessionOptions,
		PermissionPolicy:     permissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
		EncodeProviderConversationID: func(providerID string) string {
			return opencodeidentity.Encode(domain.HarnessOpenCodeV2, providerID)
		},
		DecodeProviderConversationID: func(providerID string) (string, error) {
			return opencodeidentity.Decode(domain.HarnessOpenCodeV2, providerID)
		},
	}, log)
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	content, err := opencodev2.PrepareACPConfigContent(
		cfg.Env["OPENCODE_CONFIG_CONTENT"], cfg.SystemPrompt, cfg.Permissions)
	if err != nil {
		return nil, nil, err
	}
	dataHome, err := opencodev2.DataHome(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("opencode-v2 ACP: prepare data home: %w", err)
	}
	env := map[string]string{"OPENCODE_CONFIG_CONTENT": content, "XDG_DATA_HOME": dataHome}
	return []string{"acp"}, env, nil
}

func sessionMode(ports.PermissionMode) string {
	return "build"
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := make([]acpdriver.SessionOption, 0, 2)
	if settings.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model})
	}
	if settings.Effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "effort", Value: settings.Effort})
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Model == "" {
		return nil
	}
	provider, model, found := strings.Cut(settings.Model, "/")
	if !found || provider == "" || model == "" ||
		strings.TrimSpace(provider) != provider || strings.TrimSpace(model) != model {
		return fmt.Errorf("%w: OpenCode 2 model %q must use provider/model format (for example, anthropic/claude-sonnet); select a full model ID from `opencode models`, or clear the model override to use agent settings", ports.ErrChatConfigOptionInvalid, settings.Model)
	}
	return nil
}

func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	mode = ports.NormalizePermissionMode(mode)
	kind := params.ToolCall.Kind
	if mode != ports.PermissionModeAuto && mode != ports.PermissionModeBypassPermissions &&
		(mode != ports.PermissionModeAcceptEdits || kind == nil ||
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
