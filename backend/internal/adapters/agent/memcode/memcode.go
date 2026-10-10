// Package memcode integrates the official memcode TUI. Native interactive
// /resume is deliberately separate from its broken mount-time --resume route.
package memcode

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Plugin implements the official native TUI adapter.
type Plugin struct{ agentbase.Base }

// New constructs the native TUI adapter.
func New() *Plugin { return &Plugin{} }

// Manifest implements the native TUI adapter contract.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "memcode", Name: "memcode", Description: "Official memcode terminal sessions", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

var binarySpec = binaryutil.BinarySpec{Label: "memcode", Names: []string{"memcode"}, WinNames: []string{"memcode.exe"}, UnixHomePaths: [][]string{{".local", "bin", "memcode"}}, ValidateIdentity: func(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := process.CommandContext(ctx, path, "--version").CombinedOutput()
	return err == nil && strings.Contains(string(b), "memcode")
}}

// ResolveBinary implements the native TUI adapter contract.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binarySpec)
}

// GetLaunchCommand implements the native TUI adapter contract.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if cfg.Config.Mode != "" || cfg.Config.Effort != "" {
		return nil, errors.New("memcode mode/effort overrides are unsupported")
	}
	if len(cfg.AllowedTools) > 0 || len(cfg.DisallowedTools) > 0 {
		return nil, errors.New("memcode per-launch tool restrictions are unsupported")
	}
	if err := validateModel(cfg.WorkspacePath, cfg.Config.Model); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{binary, "run"}
	switch cfg.Permissions {
	case ports.PermissionModeAcceptEdits:
		return nil, errors.New("memcode does not support accept-edits")
	case ports.PermissionModeAuto:
		args = append(args, "--auto")
	case ports.PermissionModeBypassPermissions:
		args = append(args, "--allow-all")
	default:
		args = append(args, "--ask")
	}
	return args, nil
}

// GetPromptDeliveryStrategy implements the native TUI adapter contract.
func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, ctx.Err()
}

// GetRestoreCommand rejects restore paths without the verified interactive handshake.
func (*Plugin) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, errors.New("memcode interactive native restore initialization required")
}

// SessionInfo implements the native TUI adapter contract.
func (*Plugin) SessionInfo(ctx context.Context, s ports.SessionRef) (ports.SessionInfo, bool, error) {
	info, ok := agentbase.StandardSessionInfo(s)
	return info, ok, ctx.Err()
}

// GetConfigSpec implements the native TUI adapter contract.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "AO model overrides are unsupported; configure the native endpoint/account model before launch.")
}

// AuthStatus implements the native TUI adapter contract.
func (*Plugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	if os.Getenv("MEMCODE_ENDPOINT_URL") != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}
func validateModel(_, model string) error {
	if strings.TrimSpace(model) != "" {
		return errors.New("memcode AO model overrides are unsupported; select the model in native endpoint/account configuration before launch")
	}
	return nil
}
