package commandcode

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

const adapterID = "command-code"

var commandCodeBinarySpec = binaryutil.BinarySpec{
	Label:         "command-code",
	Names:         []string{"cmd", "command-code"},
	WinNames:      []string{"cmdc.cmd", "cmdc.exe", "cmdc", "command-code.cmd", "command-code.exe", "command-code"},
	UnixPaths:     []string{"/usr/local/bin/cmd", "/opt/homebrew/bin/cmd", "/usr/local/bin/command-code", "/opt/homebrew/bin/command-code"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("cmd", []string{".commandcode", "bin", "cmd"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "cmdc.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "cmdc.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "command-code.cmd"}},
		{Base: binaryutil.WinHome, Parts: []string{".commandcode", "bin", "cmdc.exe"}},
	},
}

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
		Name:        "Command Code",
		Description: "Run Command Code interactive TUI sessions.",
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
	return ports.ConfigSpec{Fields: []ports.ConfigField{
		{Key: "model", Type: ports.ConfigFieldString, Description: "Model override passed to `cmd --model`."},
		{Key: "effort", Type: ports.ConfigFieldString, Description: "Reasoning effort override passed to `cmd --effort`."},
	}}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.binary(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := PrepareSessionHome(cfg.DataDir, domain.SessionID(cfg.SessionID), cfg.SystemPrompt); err != nil {
		return nil, err
	}

	cmd = []string{binary, "--skip-onboarding", "--no-auto-update", "--trust"}
	appendPermissionFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	appendEffortFlag(&cmd, cfg.Config)
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
		Patterns:     []string{"Command Code"},
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

	binary, err := p.binary(ctx)
	if err != nil {
		return nil, false, err
	}

	if _, err := PrepareSessionHome(cfg.DataDir, domain.SessionID(cfg.Session.ID), cfg.SystemPrompt); err != nil {
		return nil, false, err
	}
	MigrateNativeHistory(cfg.DataDir, domain.SessionID(cfg.Session.ID), agentSessionID)

	cmd = make([]string, 0, 8)
	cmd = append(cmd, binary, "--skip-onboarding", "--no-auto-update", "--trust")
	appendPermissionFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	appendEffortFlag(&cmd, cfg.Config)
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

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.binary(ctx)
}

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, _ string) {
	for key, value := range SessionProcessEnv(dataDir, sessionID, env) {
		env[key] = value
	}
}

func (p *Plugin) binary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, commandCodeBinarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func appendEffortFlag(cmd *[]string, cfg ports.AgentConfig) {
	if effort := strings.TrimSpace(cfg.Effort); effort != "" {
		*cmd = append(*cmd, "--effort", effort)
	}
}

func appendPermissionFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		*cmd = append(*cmd, "--permission-mode", "accept-edits")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--yolo")
	}
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
