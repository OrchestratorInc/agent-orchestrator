package vibe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "vibe"

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
		Name:        "Mistral Vibe",
		Description: "Run Mistral Vibe worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.vibeBinary(ctx)
	if err != nil {
		return nil, err
	}

	agentName, addDir, err := vibeAgentFlag(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config.Model, cfg.DataDir, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	cmd = make([]string, 0, 6)
	cmd = append(cmd, binary, "--trust")
	appendWorkdirFlag(&cmd, cfg.WorkspacePath)
	if addDir != "" {
		cmd = append(cmd, "--add-dir", addDir)
	}
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
		appendCustomAgentApprovalFlags(&cmd, cfg.Permissions)
	} else {
		appendAgentFlags(&cmd, cfg.Permissions)
	}
	if strings.TrimSpace(cfg.Prompt) != "" {
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

	binary, err := p.vibeBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	agentName, addDir, err := vibeAgentFlag(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config.Model, cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary, "--trust"}
	appendWorkdirFlag(&cmd, cfg.Session.WorkspacePath)
	if addDir != "" {
		cmd = append(cmd, "--add-dir", addDir)
	}
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
		appendCustomAgentApprovalFlags(&cmd, cfg.Permissions)
	} else {
		appendAgentFlags(&cmd, cfg.Permissions)
	}
	cmd = append(cmd, "--resume", agentSessionID)
	return cmd, true, nil
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
				Description: "Model override written to generated Vibe agent config (`active_model`).",
			},
		},
	}, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func appendWorkdirFlag(cmd *[]string, workspacePath string) {
	if workspacePath != "" {
		*cmd = append(*cmd, "--workdir", workspacePath)
	}
}

func appendAgentFlags(cmd *[]string, mode ports.PermissionMode) {
	switch mode {
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--agent", "accept-edits")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--agent", "auto-approve")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--agent", "auto-approve")
	}
}

func appendCustomAgentApprovalFlags(cmd *[]string, mode ports.PermissionMode) {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--auto-approve")
	}
}

const vibePromptAgentName = "ao-system-prompt"

func vibeAgentFlag(mode ports.PermissionMode, inlinePrompt, promptFile, model, dataDir, sessionID string) (string, string, error) {
	trimmedModel := strings.TrimSpace(model)
	hasPrompt := inlinePrompt != "" || promptFile != ""
	if !hasPrompt && trimmedModel == "" {
		return "", "", nil
	}
	if inlinePrompt != "" && strings.TrimSpace(promptFile) == "" {
		return "", "", fmt.Errorf("vibe: system prompt file required to build agent config")
	}
	vibeRoot, err := vibeAgentRoot(promptFile, dataDir, sessionID)
	if err != nil {
		return "", "", err
	}
	promptsDir := filepath.Join(vibeRoot, ".vibe", "prompts")
	agentsDir := filepath.Join(vibeRoot, ".vibe", "agents")
	promptText := inlinePrompt
	if promptText == "" && promptFile != "" {
		data, err := os.ReadFile(promptFile) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return "", "", err
		}
		promptText = string(data)
	}
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		return "", "", fmt.Errorf("vibe: create agents dir: %w", err)
	}
	if hasPrompt {
		if err := os.MkdirAll(promptsDir, 0o700); err != nil {
			return "", "", fmt.Errorf("vibe: create prompts dir: %w", err)
		}
		if err := hookutil.AtomicWriteFile(filepath.Join(promptsDir, vibePromptAgentName+".md"), []byte(strings.TrimRight(promptText, "\n")+"\n"), 0o600); err != nil {
			return "", "", fmt.Errorf("vibe: write prompt: %w", err)
		}
	}
	agentConfig, err := vibeAgentTOML(mode, trimmedModel, promptText, hasPrompt)
	if err != nil {
		return "", "", err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(agentsDir, vibePromptAgentName+".toml"), []byte(agentConfig), 0o600); err != nil {
		return "", "", fmt.Errorf("vibe: write agent config: %w", err)
	}
	return vibePromptAgentName, vibeRoot, nil
}

func vibeAgentRoot(promptFile, dataDir, sessionID string) (string, error) {
	if strings.TrimSpace(promptFile) != "" {
		return filepath.Join(filepath.Dir(promptFile), "vibe"), nil
	}
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("vibe: data dir and session id required to build agent config")
	}
	return vibeManagerOwnedAgentRoot(dataDir, sessionID), nil
}

func vibeManagerOwnedAgentRoot(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "prompts", sessionID, "vibe")
}

func vibeAgentTOML(mode ports.PermissionMode, model, promptText string, hasPrompt bool) (string, error) {
	var b strings.Builder
	b.WriteString(`agent_type = "agent"` + "\n")
	b.WriteString(`display_name = "Orchestrator Session"` + "\n")
	b.WriteString(`description = "Orchestrator-managed session standing instructions."` + "\n")
	b.WriteString(`safety = "neutral"` + "\n")
	if hasPrompt {
		instructions, err := vibeTOMLBasicString(promptText)
		if err != nil {
			return "", fmt.Errorf("vibe: encode standing instructions: %w", err)
		}
		b.WriteString("instructions = ")
		b.WriteString(instructions)
		b.WriteString("\n")
	}
	if model != "" {
		activeModel, err := vibeTOMLBasicString(model)
		if err != nil {
			return "", fmt.Errorf("vibe: encode active model: %w", err)
		}
		b.WriteString("active_model = ")
		b.WriteString(activeModel)
		b.WriteString("\n")
	}
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeAcceptEdits {
		b.WriteString("\n[tools.write_file]\npermission = \"always\"\n")
		b.WriteString("\n[tools.search_replace]\npermission = \"always\"\n")
	}
	return b.String(), nil
}

func vibeTOMLBasicString(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("invalid UTF-8")
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

var vibeBinarySpec = binaryutil.BinarySpec{
	Label:         "vibe",
	Names:         []string{"vibe"},
	WinNames:      []string{"vibe.exe", "vibe.cmd", "vibe"},
	UnixPaths:     []string{"/usr/local/bin/vibe", "/opt/homebrew/bin/vibe"},
	UnixHomePaths: [][]string{{".local", "bin", "vibe"}, {".local", "share", "uv", "tools", "mistral-vibe", "bin", "vibe"}},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"Python", "Scripts", "vibe.exe"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"uv", "tools", "mistral-vibe", "Scripts", "vibe.exe"}},
	},
}

func ResolveVibeBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, vibeBinarySpec)
}

func (p *Plugin) vibeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveVibeBinary(ctx)
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
