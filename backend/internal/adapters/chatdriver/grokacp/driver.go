package grokacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessGrok,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityHistory: true,
		},
		Configure:            configure,
		SessionOptions:       sessionOptions,
		SessionMeta:          sessionMeta,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if cfg.DisableTools {
		return nil, nil, fmt.Errorf("%w: Grok ACP cannot disable native tools", ports.ErrChatCapabilityUnavailable)
	}
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	args := []string{"agent"}
	if ports.NormalizePermissionMode(cfg.Permissions) == ports.PermissionModeBypassPermissions {
		args = append(args, "--always-approve")
	}
	return append(args, "stdio"), nil, nil
}

func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	initial = ports.NormalizePermissionMode(initial)
	requested := initial
	if settings.Approval != "" {
		requested = ports.NormalizePermissionMode(settings.Approval)
	}
	switch requested {
	case ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
	default:
		return fmt.Errorf("%w: Grok ACP does not offer permission mode %q", ports.ErrChatPermissionModeUnsupported, requested)
	}
	if requested != initial {
		return fmt.Errorf("%w: changing Grok ACP permissions from %q to %q requires restarting Chat",
			ports.ErrChatPermissionModeUnsupported, initial, requested)
	}
	return nil
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "reasoning_effort", Value: effort})
	}
	return options
}

func sessionMeta(cfg acpdriver.LaunchConfig) map[string]any {
	var meta map[string]any
	if ports.NormalizePermissionMode(cfg.Permissions) == ports.PermissionModeAuto {
		meta = map[string]any{"autoMode": true}
	}
	if rules := strings.TrimSpace(cfg.SystemPrompt); rules != "" {
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["rules"] = rules
	}
	return meta
}
