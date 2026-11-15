package pi

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "pi"

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Pi",
		Description: "Run Pi worker sessions.",
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
				Description: "Model override passed to `pi --model`.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.piBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendPiSessionDirFlag(&cmd, cfg.DataDir)
	appendPiExtensionFlag(&cmd, cfg.WorkspacePath)
	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", cfg.SystemPrompt)
	} else if cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return nil, err
		}
		cmd = append(cmd, "--append-system-prompt", string(data))
	}
	appendModelFlag(&cmd, cfg.Config)
	if cfg.Prompt != "" {
		cmd = append(cmd, cfg.Prompt)
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

	binary, err := p.piBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary}
	if piTranscriptInManagedDir(cfg.Session.Metadata[ports.MetadataKeyNativeTranscriptPath], cfg.DataDir) {
		appendPiSessionDirFlag(&cmd, cfg.DataDir)
	}
	appendPiExtensionFlag(&cmd, cfg.Session.WorkspacePath)
	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", cfg.SystemPrompt)
	} else if cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return nil, false, err
		}
		cmd = append(cmd, "--append-system-prompt", string(data))
	}
	appendModelFlag(&cmd, cfg.Config)
	cmd = append(cmd, "--session", agentSessionID)
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
	if effort := strings.TrimSpace(cfg.Effort); effort != "" {
		*cmd = append(*cmd, "--thinking", effort)
	}
}

var piBinarySpec = binaryutil.BinarySpec{
	Label:         "pi",
	Names:         []string{"pi"},
	WinNames:      []string{"pi.cmd", "pi.exe", "pi"},
	UnixPaths:     []string{"/usr/local/bin/pi", "/opt/homebrew/bin/pi"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("pi", []string{".pi", "bin", "pi"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "pi.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "pi.exe"}},
	},
}

func ResolvePiBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, piBinarySpec)
}

func (p *Plugin) piBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolvePiBinary(ctx)
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

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
