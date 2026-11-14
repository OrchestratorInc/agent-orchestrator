package aider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	adapterID                = "aider"
	aiderNotificationCommand = "ao hooks aider notification"
)

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.WaitingInputComposerReadiness = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Aider",
		Description: "Run Aider worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `aider --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.aiderBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	cmd = append(cmd,
		"--no-check-update", "--no-stream", "--no-pretty",
		"--notifications", "--notifications-command", aiderNotificationCommand,
	)
	if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--read", cfg.SystemPromptFile)
	}
	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

func normalizePermissionMode(mode ports.PermissionMode) ports.PermissionMode {
	if mode == "" {
		return ports.PermissionModeDefault
	}
	return mode
}

func appendApprovalFlags(cmd *[]string, mode ports.PermissionMode) {
	switch normalizePermissionMode(mode) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--yes-always", "--no-auto-commits")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--yes-always")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--yes-always")
	default:
	}
}

func ResolveAiderBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if managed, ok := binaryutil.ManagedBinary("aider"); ok {
		return managed, nil
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"aider.exe", "aider.cmd", "aider"} {
			if path, err := exec.LookPath(name); err == nil && path != "" {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		for _, candidate := range binaryutil.WindowsPackageManagerBinCandidates("aider") {
			if hookutil.IsExecutableFile(candidate) {
				return candidate, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		return "", fmt.Errorf("aider: %w", ports.ErrAgentBinaryNotFound)
	}

	if path, err := exec.LookPath("aider"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/aider",
		"/opt/homebrew/bin/aider",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{filepath.Join(home, ".local", "bin", "aider")}, candidates...)
		candidates = append(candidates, binaryutil.UnixPackageManagerBinCandidates(home, "aider")...)
	}

	for _, candidate := range candidates {
		if hookutil.IsExecutableFile(candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("aider: %w", ports.ErrAgentBinaryNotFound)
}

func (p *Plugin) aiderBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveAiderBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
