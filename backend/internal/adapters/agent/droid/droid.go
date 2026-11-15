package droid

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
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
		ID:          "droid",
		Name:        "Droid",
		Description: "Run Factory Droid worker sessions.",
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
				Description: "Model override written to Droid's runtime settings file.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.droidBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = make([]string, 0, 6)
	cmd = append(cmd, binary)

	settingsArgs, err := runtimeSettingsArgs(cfg.DataDir, cfg.SessionID, cfg.Permissions, cfg.Config.Model)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, settingsArgs...)

	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", cfg.SystemPrompt)
	} else if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--append-system-prompt-file", cfg.SystemPromptFile)
	}

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

	binary, err := p.droidBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 5)
	cmd = append(cmd, binary)
	settingsArgs, err := runtimeSettingsArgs(cfg.DataDir, cfg.Session.ID, cfg.Permissions, cfg.Config.Model)
	if err != nil {
		return nil, false, err
	}
	cmd = append(cmd, settingsArgs...)
	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", cfg.SystemPrompt)
	} else if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--append-system-prompt-file", cfg.SystemPromptFile)
	}
	cmd = append(cmd, "-r", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func droidAutonomyLevel(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return "low"
	case ports.PermissionModeAuto:
		return "medium"
	case ports.PermissionModeBypassPermissions:
		return "high"
	default:
		return ""
	}
}

func runtimeSettingsArgs(dataDir, sessionID string, mode ports.PermissionMode, model string) ([]string, error) {
	level := droidAutonomyLevel(mode)
	model = strings.TrimSpace(model)
	if level == "" && model == "" {
		return nil, nil
	}
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("droid: Orchestrator data directory required for runtime settings")
	}

	settings := map[string]any{}
	if level != "" {
		settings["sessionDefaultSettings"] = map[string]any{"autonomyLevel": level}
	}
	if model != "" {
		settings["model"] = model
	}

	blob, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("droid: encode runtime settings: %w", err)
	}

	path := runtimeSettingsPath(dataDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("droid: create runtime settings dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(path, append(blob, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("droid: write runtime settings: %w", err)
	}
	return []string{"--settings", path}, nil
}

func PrepareRuntimeSettingsArgs(
	dataDir, sessionID string,
	mode ports.PermissionMode,
	model string,
) ([]string, error) {
	return runtimeSettingsArgs(dataDir, sessionID, mode, model)
}

func runtimeSettingsPath(dataDir, sessionID string) string {
	name := sanitizeSessionID(sessionID)
	if name == "" {
		name = "default"
	}
	return filepath.Join(dataDir, "agent-runtime", "droid", "ao-droid-"+name+"-settings.json")
}

func sanitizeSessionID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

var droidBinarySpec = binaryutil.BinarySpec{
	Label:         "droid",
	Names:         []string{"droid"},
	WinNames:      []string{"droid.cmd", "droid.exe", "droid"},
	UnixPaths:     []string{"/usr/local/bin/droid", "/opt/homebrew/bin/droid"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("droid", []string{".factory", "bin", "droid"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "droid.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "droid.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "droid.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".factory", "bin", "droid.exe"}},
	},
}

func ResolveDroidBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, droidBinarySpec)
}

func (p *Plugin) droidBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveDroidBinary(ctx)
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
