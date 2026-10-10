// Package openinterpreter integrates the Rust Open Interpreter terminal CLI.
// Native hooks supply private context and identity; the terminal observer owns
// activity because prompt/stop/permission hooks precede native policy decisions.
package openinterpreter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// InspectedRelease identifies the released Rust CLI used for this contract.
const InspectedRelease = "rust-v0.0.56"

// InspectedCommit pins the source corresponding to InspectedRelease.
const InspectedCommit = "cc054cf52fa3585a3de50e0d4e0be6f9ee6677e8"

// Plugin implements the Open Interpreter TUI adapter.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns an Open Interpreter adapter.
func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)

// Manifest describes the terminal-only integration.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "open-interpreter", Name: "Open Interpreter", Description: "Run Rust Open Interpreter terminal sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes native free-form model selection.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model ID passed to interpreter --model; configure its provider in Open Interpreter.")
}

// GetLaunchCommand gives the initial task to the native startup queue. A task
// beginning with a dash is data after --, and is never typed into onboarding.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 {
		return nil, fmt.Errorf("open-interpreter: tool allow/deny restrictions are not supported")
	}
	return p.command(ctx, "", cfg.WorkspacePath, cfg.Prompt, cfg.Config, cfg.Permissions)
}

// GetRestoreCommand resumes only the exact provider UUID. Missing identity is
// an error so a restore cannot silently start a different conversation.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	id, err := nativeID(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if err != nil {
		return nil, false, err
	}
	if len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 {
		return nil, false, fmt.Errorf("open-interpreter: tool allow/deny restrictions are not supported")
	}
	if err := p.validateRestoreWorkspace(ctx, cfg, id); err != nil {
		return nil, false, err
	}
	cmd, err := p.command(ctx, id, cfg.Session.WorkspacePath, cfg.Prompt, cfg.Config, cfg.Permissions)
	return cmd, err == nil, err
}

func (p *Plugin) command(ctx context.Context, resume, workspace, prompt string, config ports.AgentConfig, permission ports.PermissionMode) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flags, err := permissionArgs(permission)
	if err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary}
	if resume != "" {
		cmd = append(cmd, "resume", resume)
	}
	// --no-daemon disables both shared-server reuse and automatic startup.
	cmd = append(cmd, "--no-daemon", "-c", "check_for_update_on_startup=false")
	cmd = append(cmd, flags...)
	if workspace != "" {
		cmd = append(cmd, "--cd", workspace)
	}
	agentbase.AppendModelFlag(&cmd, config, "--model")
	if effort := strings.TrimSpace(config.Effort); effort != "" {
		cmd = append(cmd, "-c", "model_reasoning_effort="+tomlString(effort))
	}
	if err := appendSessionHooks(&cmd); err != nil {
		return nil, err
	}
	if prompt != "" {
		cmd = append(cmd, "--", prompt)
	}
	return cmd, nil
}

func permissionArgs(mode ports.PermissionMode) ([]string, error) {
	switch mode {
	case "", ports.PermissionModeDefault:
		return nil, nil
	case ports.PermissionModeAcceptEdits:
		return []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}, nil
	case ports.PermissionModeAuto:
		return []string{"--auto-review"}, nil
	case ports.PermissionModeBypassPermissions:
		return []string{"--dangerously-bypass-approvals-and-sandbox"}, nil
	default:
		return nil, fmt.Errorf("open-interpreter: unsupported permission mode %q", mode)
	}
}

func nativeID(value string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || id == uuid.Nil {
		return "", fmt.Errorf("open-interpreter: exact native session UUID is required")
	}
	return id.String(), nil
}

// SessionInfo reads native identity captured by the session hooks.
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(ref)
	return info, ok, nil
}

// ExitDetectionMode uses AO's supervisor for confirmed process exit.
func (p *Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// EmitsBlockedActivity disables Enter resubmission nudges at native dialogs.
func (p *Plugin) EmitsBlockedActivity() bool { return false }

// ContinuouslyDetectTerminalActivity observes human submits and settled turns.
func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// ResolveBinary selects the public interpreter binary, never the ambiguous i alias.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{
		Label: "Open Interpreter (Rust)", Names: []string{"interpreter"}, WinNames: []string{"interpreter.exe", "interpreter"},
		UnixPaths:     []string{"/usr/local/bin/interpreter", "/opt/homebrew/bin/interpreter"},
		UnixHomePaths: [][]string{{".local", "bin", "interpreter"}},
		WinPaths:      []binaryutil.WinPath{{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "interpreter.exe"}}},
	})
	if err != nil {
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	probe := aoprocess.CommandContext(probeCtx, binary, "--help")
	probe.WaitDelay = 2 * time.Second
	output, err := probe.CombinedOutput()
	if err != nil || !isRustCLIHelp(string(output)) {
		return "", fmt.Errorf("open-interpreter: install the Rust terminal CLI (%s); the Python interpreter command is incompatible", InspectedRelease)
	}
	p.resolvedBinary = binary
	return binary, nil
}

func isRustCLIHelp(output string) bool {
	return strings.Contains(output, "Open Interpreter") && strings.Contains(output, "--no-daemon") && strings.Contains(output, "--chat-completions") && strings.Contains(output, "resume")
}

// InvalidateBinaryResolution refreshes discovery after a provider upgrade.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	p.resolvedBinary = ""
}
