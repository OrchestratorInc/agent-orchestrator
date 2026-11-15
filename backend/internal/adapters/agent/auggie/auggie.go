package auggie

import (
	"context"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "auggie"

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
		ID:          adapterID,
		Name:        "Auggie",
		Description: "Run Auggie (Augment Code) worker sessions.",
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
				Description: "Model override passed to `auggie --model`.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.auggieBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary, "--print"}
	if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--rules", cfg.SystemPromptFile)
	}
	appendModelFlag(&cmd, cfg.Config)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.auggieBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary}
	if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--rules", cfg.SystemPromptFile)
	}
	appendModelFlag(&cmd, cfg.Config)
	cmd = append(cmd, "--resume", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, "--model", model)
	}
}

var auggieBinarySpec = binaryutil.BinarySpec{
	Label:         "auggie",
	Names:         []string{"auggie"},
	WinNames:      []string{"auggie.cmd", "auggie.exe", "auggie"},
	UnixPaths:     []string{"/usr/local/bin/auggie", "/opt/homebrew/bin/auggie"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("auggie"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "auggie.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "auggie.exe"}},
	},
}

func ResolveAuggieBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, auggieBinarySpec)
}

func (p *Plugin) auggieBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveAuggieBinary(ctx)
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
