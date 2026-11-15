package mimocode

import (
	"context"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "mimo-code"

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)

func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID: adapterID, Name: "MiMo Code", Description: "Run MiMo Code worker sessions.", Version: "0.0.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `mimo --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := p.mimoBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "--trust"}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if usesAOAgent(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile) {
		cmd = append(cmd, "--agent", mimoAgentName(cfg.SessionID))
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	nativeID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if !validNativeSessionID(nativeID) {
		return nil, false, nil
	}
	binary, err := p.mimoBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd := []string{binary, "--trust"}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if usesAOAgent(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile) {
		cmd = append(cmd, "--agent", mimoAgentName(cfg.Session.ID))
	}
	cmd = append(cmd, "--session", nativeID)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func validNativeSessionID(id string) bool {
	return strings.HasPrefix(id, "ses_") && len(id) > len("ses_")
}

func usesAOAgent(mode ports.PermissionMode, inlinePrompt, promptFile string) bool {
	return mimoAgentPermission(mode) != "" || inlinePrompt != "" || promptFile != ""
}

func mimoAgentPermission(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return "permission:\n  edit: allow\n"
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return "permission:\n  \"*\": allow\n"
	}
	return ""
}

func mimoAgentName(sessionID string) string {
	const fallback = "ao-system-prompt"
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" {
		return fallback
	}
	return "ao-" + name
}

var mimoBinarySpec = binaryutil.BinarySpec{
	Label:         "mimo",
	Names:         []string{"mimo"},
	WinNames:      []string{"mimo.cmd", "mimo.exe", "mimo"},
	UnixPaths:     []string{"/usr/local/bin/mimo", "/opt/homebrew/bin/mimo"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("mimo"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "mimo.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "mimo.exe"}},
	},
}

func ResolveMiMoCodeBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, mimoBinarySpec)
}

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) { return p.mimoBinary(ctx) }

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func (p *Plugin) mimoBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := ResolveMiMoCodeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}
