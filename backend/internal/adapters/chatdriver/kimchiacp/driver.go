package kimchiacp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimchi"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:        domain.HarnessKimchi,
		Configure:      configure,
		SessionOptions: sessionOptions,
		SteerRequest:   steerRequest,
		VersionProbe:   versionProbe,
	}, log)
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if _, err := kimchi.New().NativeSessionConfigDir(ctx, cfg.Env); err != nil {
		return nil, nil, err
	}
	args := []string{"--mode", "acp"}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		args = append(args, "--model", model)
	}
	appendPermissionFlags(&args, cfg.Permissions)
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		file, err := acpdriver.SystemPromptFile(cfg, "kimchi")
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--append-system-prompt", file)
	}
	return args, nil, nil
}

func appendPermissionFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--yolo")
	}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if settings.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model})
	}
	if mode := kimchiPermissionMode(settings.Approval); mode != "" {
		options = append(options, acpdriver.SessionOption{ID: "permissions-mode", Value: mode})
	}
	return options
}

func kimchiPermissionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "yolo"
	default:
		return ""
	}
}
