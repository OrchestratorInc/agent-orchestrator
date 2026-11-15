package kimiacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessKimi,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityHistory: true,
			ports.ChatCapabilityPlans:   true,
		},
		Configure:            configure,
		SessionPreamble:      sessionPreamble,
		SessionOptions:       sessionOptions,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	if err := kimi.PrepareACPInstructions(ctx, cfg.WorkspacePath, cfg.SystemPrompt); err != nil {
		return nil, nil, err
	}
	if err := kimi.ShareCredentials(cfg.DataDir); err != nil {
		return nil, nil, err
	}
	return []string{"acp"}, nil, nil
}

func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" || !kimi.CodeInstructionsOwnedByProject(context.Background(), cfg.WorkspacePath) {
		return nil
	}
	return []acpsdk.ContentBlock{acpdriver.StandingInstructionsBlock(standing)}
}

func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if mode := ports.NormalizePermissionMode(settings.Approval); mode != ports.PermissionModeDefault {
		return fmt.Errorf("%w: Kimi ACP does not offer other session modes; requested %q",
			ports.ErrChatPermissionModeUnsupported, mode)
	}
	return nil
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if settings.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model})
	}
	return options
}
