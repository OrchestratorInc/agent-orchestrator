package kilocode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	adapterID = "kilocode"
)

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
		Name:        "Kilo Code",
		Description: "Run Kilo Code worker sessions.",
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
				Description: "Model override written to the Orchestrator-generated Kilo agent (agent.<name>.model); format provider/model-id (e.g. anthropic/claude-haiku-4-20250514).",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.kilocodeBinary(ctx)
	if err != nil {
		return nil, err
	}

	envPrefix, agentName, err := kilocodeConfigEnvPrefix(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.SessionID, cfg.Config.Model)
	if err != nil {
		return nil, err
	}
	cmd = envPrefix
	cmd = append(cmd, binary)
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
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

	binary, err := p.kilocodeBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	envPrefix, agentName, err := kilocodeConfigEnvPrefix(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Session.ID, cfg.Config.Model)
	if err != nil {
		return nil, false, err
	}
	cmd = envPrefix
	cmd = append(cmd, binary)
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
	}
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

const kilocodePermissionEnvVar = "KILO_CONFIG_CONTENT"

func kilocodePermissionConfig(mode ports.PermissionMode) map[string]string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return map[string]string{"edit": "allow"}
	case ports.PermissionModeAuto:
		return map[string]string{"edit": "allow", "bash": "allow"}
	case ports.PermissionModeBypassPermissions:
		return map[string]string{"*": "allow"}
	default:
		return nil
	}
}

type kilocodeInlineConfig struct {
	Permission map[string]string                `json:"permission,omitempty"`
	Agent      map[string]kilocodeAgentSettings `json:"agent,omitempty"`
}

type kilocodeAgentSettings struct {
	Prompt string `json:"prompt,omitempty"`
	Model  string `json:"model,omitempty"`
}

func kilocodeConfigEnvPrefix(mode ports.PermissionMode, inlinePrompt, promptFile, sessionID, model string) ([]string, string, error) {
	config := kilocodeInlineConfig{Permission: kilocodePermissionConfig(mode)}
	agentName := ""
	systemPrompt, err := kilocodeSystemPromptText(inlinePrompt, promptFile)
	if err != nil {
		return nil, "", err
	}
	model = strings.TrimSpace(model)
	if systemPrompt != "" || model != "" {
		agentName = kilocodeAOAgentName(sessionID)
		config.Agent = map[string]kilocodeAgentSettings{
			agentName: {Prompt: systemPrompt, Model: model},
		}
	}
	if len(config.Permission) == 0 && len(config.Agent) == 0 {
		return nil, "", nil
	}
	blob, err := json.Marshal(config)
	if err != nil {
		return nil, "", err
	}
	return []string{"env", kilocodePermissionEnvVar + "=" + string(blob)}, agentName, nil
}

func kilocodeSystemPromptText(inline, file string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("kilocode: read system prompt file: %w", err)
	}
	return string(data), nil
}

func kilocodeAOAgentName(sessionID string) string {
	const fallback = "ao-system-prompt"
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-',
			r == '_':
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

var kilocodeBinarySpec = binaryutil.BinarySpec{
	Label:         "kilocode",
	Names:         []string{"kilo", "kilocode"},
	WinNames:      []string{"kilo.cmd", "kilo.exe", "kilo", "kilocode.cmd", "kilocode.exe", "kilocode"},
	UnixPaths:     []string{"/usr/local/bin/kilo", "/opt/homebrew/bin/kilo", "/usr/local/bin/kilocode", "/opt/homebrew/bin/kilocode"},
	UnixHomePaths: append(binaryutil.NodeManagedUnixHomePaths("kilo"), binaryutil.NodeManagedUnixHomePaths("kilocode")...),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kilo.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kilo.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kilocode.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kilocode.exe"}},
	},
}

func ResolveKilocodeBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, kilocodeBinarySpec)
}

func (p *Plugin) kilocodeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveKilocodeBinary(ctx)
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
