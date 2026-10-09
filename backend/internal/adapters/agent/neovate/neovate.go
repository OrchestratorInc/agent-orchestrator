// Package neovate adapts the Neovate Code terminal CLI.
package neovate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin drives Neovate's interactive terminal interface. Chat is independent.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New constructs the Neovate adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)
var _ ports.AgentInterruptInputProvider = (*Plugin)(nil)

// Manifest identifies this terminal harness.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "neovate", Name: "Neovate Code", Description: "Run Neovate Code terminal sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes Neovate's provider/model selection.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model configured in Neovate, in provider/model form.")
}

// EmitsSemanticMessageAcceptance opts into native accepted-prompt correlation.
func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

// InterruptInput returns Neovate's native cancellation key without submitting.
func (p *Plugin) InterruptInput() string { return "\x1b" }

// GetLaunchCommand starts an interactive session with an argv-delivered task.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 {
		return nil, errors.New("neovate: tool-restricted launches are unsupported")
	}
	return p.command(ctx, cfg.WorkspacePath, cfg.SessionID, cfg.Config, cfg.Permissions, "", cfg.Prompt)
}

// GetRestoreCommand fails closed if the exact native transcript is unavailable.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 {
		return nil, false, errors.New("neovate: tool-restricted restores are unsupported")
	}
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if err := validateRestore(cfg.Session.WorkspacePath, cfg.Session.ID, id); err != nil {
		return nil, false, fmt.Errorf("neovate: cannot restore native session: %w", err)
	}
	cmd, err := p.command(ctx, cfg.Session.WorkspacePath, cfg.Session.ID, cfg.Config, cfg.Permissions, id, cfg.Prompt)
	return cmd, err == nil, err
}

func (p *Plugin) command(ctx context.Context, workspace, session string, cfg ports.AgentConfig, mode ports.PermissionMode, nativeID, prompt string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if workspace == "" || session == "" {
		return nil, errors.New("neovate: workspace and AO session ID are required")
	}
	if err := validateTask(prompt); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	permission := "default"
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		permission = "autoEdit"
	case ports.PermissionModeBypassPermissions:
		permission = "yolo"
	default:
		return nil, fmt.Errorf("neovate: unsupported permission mode %q", mode)
	}
	cmd := []string{binary, "--cwd", workspace, "--approval-mode", permission, "--plugin", pluginPath(workspace, session)}
	agentbase.AppendModelFlag(&cmd, cfg, "--model")
	if nativeID != "" {
		cmd = append(cmd, "--resume", nativeID)
	}
	// yargs-parser preserves the following argument as data, including '-'.
	// A single argument also preserves embedded whitespace and newlines.
	if prompt != "" {
		cmd = append(cmd, "--", prompt)
	}
	return cmd, nil
}

// SessionInfo returns the native identity captured from Neovate's own hooks.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

var binarySpec = binaryutil.BinarySpec{
	Label: "neovate", Names: []string{"neovate"}, WinNames: []string{"neovate.cmd", "neovate.exe", "neovate"},
	UnixPaths:     []string{"/usr/local/bin/neovate", "/opt/homebrew/bin/neovate"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("neovate"), NodeManaged: true,
	WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "neovate.cmd"}}},
}

// ResolveBinary locates the official npm executable without running it.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err == nil {
		p.resolvedBinary = binary
	}
	return binary, err
}

// InvalidateBinaryResolution discards the cached executable after installation.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func validNativeID(id string) bool {
	if len(id) != 8 {
		return false
	}
	return strings.Trim(id, "0123456789abcdef") == ""
}

// Neovate routes these values before its accepted-user-prompt hook. Reject them
// rather than interpreting an AO task as a native command or shell execution.
func validateTask(prompt string) error {
	if strings.HasPrefix(prompt, "!") || strings.HasPrefix(prompt, "/") {
		return errors.New("neovate: initial tasks starting with ! or / are native commands; use a plain-language task")
	}
	switch prompt {
	case "__test", "acp", "config", "commit", "mcp", "log", "run", "server", "skill", "update", "workspace":
		return fmt.Errorf("neovate: task %q is a reserved native command; use a plain-language task", prompt)
	}
	return nil
}
