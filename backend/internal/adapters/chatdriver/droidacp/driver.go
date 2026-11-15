package droidacp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:   domain.HarnessDroid,
		Configure: configure,
	}, log)
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	settingsMode := cfg.Permissions
	bypass := ports.NormalizePermissionMode(cfg.Permissions) == ports.PermissionModeBypassPermissions
	if bypass {
		settingsMode = ports.PermissionModeDefault
	}
	args, err := droid.PrepareRuntimeSettingsArgs(
		cfg.DataDir, string(cfg.SessionID), settingsMode, cfg.Model)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, "exec", "--output-format", "acp-daemon")
	if bypass {
		args = append(args, "--skip-permissions-unsafe")
	}
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		file, err := acpdriver.SystemPromptFile(cfg, "droid")
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--append-system-prompt-file", file)
	}
	return args, nil, nil
}
