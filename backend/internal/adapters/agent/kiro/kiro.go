package kiro

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "kiro",
		Name:        "Kiro",
		Description: "Run Kiro (AWS) worker sessions.",
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
				Description: "Model override written into Kiro's workspace-local agent config.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.kiroBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary, "chat"}
	cmd = append(cmd, "--agent", kiroAgentName)
	appendApprovalFlags(&cmd, cfg.Permissions)

	prompt := cfg.Prompt
	if prompt != "" && cfg.Kind == domain.KindOrchestrator {
		cmd = append(cmd, "--", prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Prompt != "" && cfg.Kind == domain.KindOrchestrator {
		return ports.PromptDeliveryInCommand, nil
	}
	if cfg.Prompt != "" && cfg.Kind != domain.KindOrchestrator {
		return ports.PromptDeliveryAfterStart, nil
	}
	if cfg.Kind == domain.KindOrchestrator {
		return ports.PromptDeliveryCustomAgent, nil
	}
	return ports.PromptDeliveryInCommand, nil
}

func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Patterns:     []string{"ask a question or describe a task"},
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

	binary, err := p.kiroBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 8)
	cmd = append(cmd, binary, "chat", "--agent", kiroAgentName, "--resume-id", agentSessionID)
	appendApprovalFlags(&cmd, cfg.Permissions)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

var kiroBinarySpec = binaryutil.BinarySpec{
	Label:         "kiro",
	Names:         []string{"kiro-cli"},
	WinNames:      []string{"kiro-cli.cmd", "kiro-cli.exe", "kiro-cli"},
	UnixPaths:     []string{"/usr/local/bin/kiro-cli", "/opt/homebrew/bin/kiro-cli"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("kiro-cli", []string{".kiro", "bin", "kiro-cli"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "kiro", "kiro-cli.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kiro-cli.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kiro-cli.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".kiro", "bin", "kiro-cli.exe"}},
	},
}

func ResolveKiroBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, kiroBinarySpec)
}

func (p *Plugin) kiroBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveKiroBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--trust-tools=fs_read,fs_write")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--trust-all-tools")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--trust-all-tools")
	}
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
