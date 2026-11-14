package autohand

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

const adapterID = "autohand"

const NoBrowserEnv = "AUTOHAND_NO_BROWSER"

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

func (p *Plugin) AugmentRuntimeEnv(env map[string]string, _ string) {
	env[NoBrowserEnv] = "1"
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Autohand",
		Description: "Run Autohand worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `autohand --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.autohandBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendWorkspaceFlag(&cmd, cfg.WorkspacePath)
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")

	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--sys-prompt", cfg.SystemPrompt)
	} else if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--sys-prompt", cfg.SystemPromptFile)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: time.Second,
		Patterns:     []string{"? shortcuts · / commands"},
		PollInterval: 250 * time.Millisecond,
		Timeout:      60 * time.Second,
		Lines:        40,
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

	binary, err := p.autohandBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 7)
	cmd = append(cmd, binary, "resume")
	appendWorkspaceFlag(&cmd, cfg.Session.WorkspacePath)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	cmd = append(cmd, agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func appendWorkspaceFlag(cmd *[]string, workspacePath string) {
	if strings.TrimSpace(workspacePath) != "" {
		*cmd = append(*cmd, "--path", workspacePath)
	}
}

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--yes")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--unrestricted")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--unrestricted")
	}
}

var autohandBinarySpec = binaryutil.BinarySpec{
	Label:         "autohand",
	Names:         []string{"autohand"},
	WinNames:      []string{"autohand.cmd", "autohand.exe", "autohand"},
	UnixPaths:     []string{"/usr/local/bin/autohand", "/opt/homebrew/bin/autohand"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("autohand"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "autohand.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "autohand.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "autohand.exe"}},
	},
}

func ResolveAutohandBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, autohandBinarySpec)
}

func (p *Plugin) autohandBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveAutohandBinary(ctx)
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
