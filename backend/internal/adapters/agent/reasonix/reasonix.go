// Package reasonix integrates the Reasonix CLI with AO's terminal sessions.
package reasonix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin drives Reasonix without changing the user's Reasonix profile.
type Plugin struct {
	agentbase.Base
	lookup func(context.Context) (string, error)
	probe  func(context.Context, string, ...string) ([]byte, error)
}

// New returns a Reasonix terminal adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest describes the terminal-only Reasonix integration.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "reasonix", Name: "Reasonix", Description: "Run Reasonix terminal sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes Reasonix's free-form provider/model identifier.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Reasonix model identifier passed to --model.")
}

// GetLaunchCommand keeps task text out of argv and supplies standing guidance separately.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	return p.command(ctx, cfg.WorkspacePath, cfg.SystemPromptFile, cfg.Permissions, cfg.Config)
}

func (p *Plugin) command(ctx context.Context, workspace, prompt string, mode ports.PermissionMode, cfg ports.AgentConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	permission, err := permissionFlag(mode)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(workspace) {
		return nil, errors.New("reasonix: an absolute workspace path is required")
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		return nil, errors.New("reasonix: workspace directory is unavailable")
	}
	if err := validatePromptFile(prompt); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "--dir", workspace, "--permission-mode", permission, "--append-system-prompt-file", prompt}
	agentbase.AppendModelFlag(&cmd, cfg, "--model")
	return cmd, nil
}

func permissionFlag(mode ports.PermissionMode) (string, error) {
	switch mode {
	case "", ports.PermissionModeDefault:
		return "read-only", nil
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		return "workspace-write", nil
	case ports.PermissionModeBypassPermissions:
		return "danger-full-access", nil
	default:
		return "", errors.New("reasonix: unsupported permission mode")
	}
}

func validatePromptFile(path string) error {
	invalid := errors.New("reasonix: a readable nonempty UTF-8 system prompt file at an absolute path is required")
	if !filepath.IsAbs(path) {
		return invalid
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return invalid
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) || strings.TrimSpace(string(data)) == "" {
		return invalid
	}
	return nil
}

// GetPromptDeliveryStrategy uses the ready interactive composer for every task.
func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

// GetRestoreCommand requires the exact hook-captured identity; fresh fallback
// would silently discard the native conversation.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if !ValidSessionID(id) {
		return nil, false, errors.New("reasonix: exact native session identity is required to restore")
	}
	cmd, err := p.command(ctx, cfg.Session.WorkspacePath, cfg.SystemPromptFile, cfg.Permissions, cfg.Config)
	if err != nil {
		return nil, false, err
	}
	return append(cmd, "--resume-exact", id), true, nil
}

// SessionInfo returns only AO's normalized hook metadata.
func (*Plugin) SessionInfo(ctx context.Context, s ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(s)
	if info.AgentSessionID != "" && !ValidSessionID(info.AgentSessionID) {
		info.AgentSessionID = ""
		ok = info.Title != "" || info.Summary != ""
	}
	return info, ok, nil
}

// ExitDetectionMode observes actual process exit; Reasonix SessionEnd also
// describes conversation rotation inside a still-running TUI.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}
