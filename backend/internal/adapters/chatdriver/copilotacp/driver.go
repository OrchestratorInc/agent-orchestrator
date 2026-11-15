package copilotacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Plugin interface {
	nativeacp.Plugin
	InstallAgentProfile(context.Context, ports.WorkspaceHookConfig) error
}

const (
	optionAgent    = "agent"
	optionAllowAll = "allow_all"
	optionModel    = "model"
)

func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, config(plugin), log)
}

func config(plugin Plugin) nativeacp.Config {
	return nativeacp.Config{
		Harness: domain.HarnessCopilot,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityUsage: true,
		},
		Configure: func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
			return configure(ctx, plugin, cfg)
		},
		LaunchSessionOptions:     launchSessionOptions,
		RequireAdvertisedOptions: true,
		CloseSessionBeforeReload: true,
		SessionOptions:           sessionOptions,
		ValidateTurnSettings:     validateTurnSettings,
		ValidateInitialize:       validateInitialize,
	}
}

func configure(ctx context.Context, plugin Plugin, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := append([]string{"--acp", "--no-auto-update"}, launchApprovalArgs(cfg.Permissions)...)
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		if err := plugin.InstallAgentProfile(ctx, ports.WorkspaceHookConfig{
			SessionID:     string(cfg.SessionID),
			WorkspacePath: cfg.WorkspacePath,
			SystemPrompt:  cfg.SystemPrompt,
		}); err != nil {
			return nil, nil, err
		}
	}
	return args, nil, nil
}

func launchApprovalArgs(permissions ports.PermissionMode) []string {
	if ports.NormalizePermissionMode(permissions) == ports.PermissionModeBypassPermissions {
		return nil
	}
	return copilot.ApprovalArgs(permissions)
}

func launchSessionOptions(cfg acpdriver.LaunchConfig) []acpdriver.SessionOption {
	name := copilot.AgentProfileName(string(cfg.SessionID), cfg.SystemPrompt, "")
	if name == "" {
		return nil
	}
	return []acpdriver.SessionOption{{ID: optionAgent, Value: name}}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: optionModel, Value: model})
	}
	if settings.Approval != "" {
		options = append(options, acpdriver.SessionOption{ID: optionAllowAll, Value: allowAll(settings.Approval)})
	}
	return options
}

func allowAll(mode ports.PermissionMode) string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeBypassPermissions {
		return "on"
	}
	return "off"
}

func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Approval == "" {
		return nil
	}
	requested := ports.NormalizePermissionMode(settings.Approval)
	baseline := baselineMode(initial)
	if requested == ports.PermissionModeBypassPermissions || requested == baseline {
		return nil
	}
	return fmt.Errorf(
		"%w: a Copilot Chat session started in %s can switch only between %s and %s; restart Chat to use %s",
		acpdriver.ErrACPSetterUnsupported, ports.NormalizePermissionMode(initial),
		baseline, ports.PermissionModeBypassPermissions, requested)
}

func baselineMode(initial ports.PermissionMode) ports.PermissionMode {
	mode := ports.NormalizePermissionMode(initial)
	if mode == ports.PermissionModeBypassPermissions {
		return ports.PermissionModeDefault
	}
	return mode
}
