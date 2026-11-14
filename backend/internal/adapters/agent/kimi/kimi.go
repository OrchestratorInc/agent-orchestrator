package kimi

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	adapterID       = "kimi"
	kimiCodeHomeEnv = "KIMI_CODE_HOME"
	kimiDataDirName = "kimi"
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

func kimiCodeHomeDir(dataDir string) string {
	return filepath.Join(dataDir, kimiDataDirName)
}

func (p *Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	if strings.TrimSpace(dataDir) == "" {
		return
	}
	env[kimiCodeHomeEnv] = kimiCodeHomeDir(dataDir)
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Kimi",
		Description: "Run Kimi CLI (Moonshot AI) worker sessions.",
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
				Description: "Model override passed to `kimi --model`.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.kimiBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = make([]string, 0, 6)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
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
		InitialDelay: 750 * time.Millisecond,
		Patterns:     []string{"│ >"},
		PollInterval: 200 * time.Millisecond,
		Timeout:      90 * time.Second,
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

	binary, err := p.kimiBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = make([]string, 0, 6)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	cmd = append(cmd, "--session", agentSessionID)
	return cmd, true, nil
}

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch normalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "-y")
	}
}

func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, "--model", model)
	}
}

func normalizePermissionMode(mode ports.PermissionMode) ports.PermissionMode {
	switch mode {
	case ports.PermissionModeDefault,
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
		ports.PermissionModeBypassPermissions:
		return mode
	default:
		return ports.PermissionModeDefault
	}
}

var kimiBinarySpec = binaryutil.BinarySpec{
	Label:         "kimi",
	Names:         []string{"kimi"},
	WinNames:      []string{"kimi.cmd", "kimi.exe", "kimi"},
	UnixPaths:     []string{"/usr/local/bin/kimi", "/opt/homebrew/bin/kimi"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("kimi", []string{".kimi-code", "bin", "kimi"}, []string{".cargo", "bin", "kimi"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kimi.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kimi.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "kimi.exe"}},
	},
}

func ResolveKimiBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, kimiBinarySpec)
}

func (p *Plugin) kimiBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveKimiBinary(ctx)
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
