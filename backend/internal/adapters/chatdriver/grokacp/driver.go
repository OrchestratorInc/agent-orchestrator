// Package grokacp binds the user's Grok Build installation to AO's ACP Chat transport.
package grokacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New launches the executable resolved by the Grok agent plugin. Grok keeps
// ownership of its cached login, model catalog, and native session history.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessGrok,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityHistory: true,
		},
		Configure:            configure,
		SessionOptions:       sessionOptions,
		SessionPreamble:      sessionPreamble,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	args := []string{"agent"}
	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		// The TUI uses --permission-mode auto/bypassPermissions. Grok's ACP
		// command has no session modes or risk-based auto selector: both map to
		// its process-wide --always-approve flag, before the stdio subcommand.
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

// Grok advertises these config options alongside its legacy models block.
// Use session/set_config_option so a model switch returns the rebuilt catalog.
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

// Grok's stdio command has no --rules flag. The transport sends this on the
// first prompt and recognizes it in loaded history to avoid repeating it.
func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	if standing := strings.TrimSpace(cfg.SystemPrompt); standing != "" {
		return []acpsdk.ContentBlock{acpdriver.StandingInstructionsBlock(standing)}
	}
	return nil
}
