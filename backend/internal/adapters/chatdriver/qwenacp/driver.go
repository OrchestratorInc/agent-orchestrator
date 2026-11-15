package qwenacp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/qwen"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return newDriver(plugin, versionProbe, log)
}

func newDriver(plugin nativeacp.Plugin, probe nativeacp.VersionProbe, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:        domain.HarnessQwen,
		Configure:      configure,
		SessionMode:    sessionMode,
		SessionOptions: sessionOptions,
		VersionProbe:   probe,
		SteerDrain:     steerDrain,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityApprovals: true,
		},
	}, log)
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"--acp"}
	qwen.AppendSessionFlags(&args, cfg.Permissions, cfg.Model)
	if prompt := strings.TrimSpace(cfg.SystemPrompt); prompt != "" {
		args = append(args, "--append-system-prompt", prompt)
	}
	return args, nil, nil
}

func sessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeDefault:
		return "default"
	case ports.PermissionModeAcceptEdits:
		return "auto-edit"
	case ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "yolo"
	default:
		return ""
	}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}
