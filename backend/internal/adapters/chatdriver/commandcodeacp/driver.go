package commandcodeacp

import (
	"context"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/commandcode"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	modeDefault   = "default"
	modeAutoAccpt = "auto-accept"
	modePlan      = "plan"
	modeDontAsk   = "dont-ask"
	modeBypass    = "bypass"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessCommandCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityUsage:           true,
			ports.ChatCapabilityDiffs:           true,
			ports.ChatCapabilityPlans:           true,
			ports.ChatCapabilityModels:          true,
			ports.ChatCapabilityConfigOptions:   true,
			ports.ChatCapabilityCompaction:      true,
			ports.ChatCapabilitySkills:          true,
			ports.ChatCapabilityImages:          true,
			ports.ChatCapabilityEmbeddedContext: true,
		},
		Configure:        configure,
		SessionMode:      sessionMode,
		SessionOptions:   sessionOptions,
		PermissionPolicy: permissionPolicy,
	}, log)
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	env, err := prepareSessionHome(cfg)
	if err != nil {
		return nil, nil, err
	}
	args := []string{
		"acp",
		"--skip-onboarding",
		"--no-auto-update",
		"--trust",
	}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		args = append(args, "--model", model)
	}
	if mode := permissionMode(cfg.Permissions); mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	return args, env, nil
}

func prepareSessionHome(cfg acpdriver.LaunchConfig) (map[string]string, error) {
	if _, err := commandcode.PrepareSessionHome(cfg.DataDir, cfg.SessionID, cfg.SystemPrompt); err != nil {
		return nil, err
	}
	commandcode.MigrateNativeHistory(cfg.DataDir, cfg.SessionID, cfg.ProviderConversationID)
	return commandcode.SessionProcessEnv(cfg.DataDir, cfg.SessionID, cfg.Env), nil
}

func sessionMode(mode ports.PermissionMode) string { return permissionMode(mode) }

func permissionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		return modeAutoAccpt
	case ports.PermissionModeBypassPermissions:
		return modeBypass
	default:
		return modeDefault
	}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "effort", Value: effort})
	}
	return options
}

func permissionPolicy(ports.PermissionMode, acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	return "", false
}
