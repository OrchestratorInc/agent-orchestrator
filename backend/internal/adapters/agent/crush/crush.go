package crush

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	adapterID = "crush"
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
var _ ports.ContinuousTerminalActivityDetector = (*Plugin)(nil)
var _ ports.WaitingTerminalActivityDetector = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Crush",
		Description: "Run Crush worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "model",
				Type:        ports.ConfigFieldString,
				Description: "Model override written into Crush's workspace-local config as the large-model selection. Must be \"<provider>/<model-id>\" (e.g. \"anthropic/claude-sonnet-4-5\"): Crush's config schema requires a provider id alongside the model id, which a bare model string doesn't carry.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.crushBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}

	if cfg.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.WorkspacePath)
	}

	if cfg.Permissions == ports.PermissionModeBypassPermissions {
		cmd = append(cmd, "--yolo")
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Prompt != "" {
		return ports.PromptDeliveryAfterStart, nil
	}
	return ports.PromptDeliveryInCommand, nil
}

func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Patterns:     []string{"Ready..."},
		PollInterval: 200 * time.Millisecond,
		Timeout:      8 * time.Second,
		Lines:        80,
	}, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.crushBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = []string{binary}

	if cfg.Session.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.Session.WorkspacePath)
	}

	if cfg.Permissions == ports.PermissionModeBypassPermissions {
		cmd = append(cmd, "--yolo")
	}

	cmd = append(cmd, "--session", agentSessionID)
	return cmd, true, nil
}

var crushBinarySpec = binaryutil.BinarySpec{
	Label:         "crush",
	Names:         []string{"crush"},
	WinNames:      []string{"crush.cmd", "crush.exe", "crush"},
	UnixPaths:     []string{"/usr/local/bin/crush", "/opt/homebrew/bin/crush"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("crush", []string{".cargo", "bin", "crush"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "crush.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "crush.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".cargo", "bin", "crush.exe"}},
	},
}

func ResolveCrushBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, crushBinarySpec)
}

func (p *Plugin) crushBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveCrushBinary(ctx)
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
