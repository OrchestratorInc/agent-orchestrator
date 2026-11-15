package omp

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "omp"

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
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "OMP",
		Description: "Run OMP interactive TUI sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `omp --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := p.ompBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd := make([]string, 0, 5)
	cmd = append(cmd, binary)
	appendOMPExtensionFlag(&cmd, cfg.WorkspacePath)
	if err := appendSystemPrompt(&cmd, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, err
	}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if effort := strings.TrimSpace(cfg.Config.Effort); effort != "" {
		cmd = append(cmd, "--thinking", effort)
	}
	if prompt := strings.TrimSpace(cfg.Prompt); prompt != "" {
		cmd = append(cmd, prompt)
	}
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.ompBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd := make([]string, 0, 5)
	cmd = append(cmd, binary)
	appendOMPExtensionFlag(&cmd, cfg.Session.WorkspacePath)
	if err := appendSystemPrompt(&cmd, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, false, err
	}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if effort := strings.TrimSpace(cfg.Config.Effort); effort != "" {
		cmd = append(cmd, "--thinking", effort)
	}
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

func (p *Plugin) NativeConversationExists(ctx context.Context, _ ports.SessionRef, nativeConversationID string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id := strings.TrimSpace(nativeConversationID)
	if id == "" {
		return false, nil
	}
	configDir := strings.TrimSpace(env["PI_CODING_AGENT_DIR"])
	if configDir == "" {
		configDir = strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, err
		}
		configDir = filepath.Join(home, ".omp", "agent")
	}
	found := false
	sessionsDir := filepath.Join(configDir, "sessions")
	err := filepath.WalkDir(sessionsDir, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_"+id+".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found, nil
}

func appendSystemPrompt(cmd *[]string, inline, file string) error {
	if inline != "" {
		*cmd = append(*cmd, "--append-system-prompt", inline)
		return nil
	}
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return err
	}
	*cmd = append(*cmd, "--append-system-prompt", string(data))
	return nil
}

var ompBinarySpec = binaryutil.BinarySpec{
	Label:         "omp",
	Names:         []string{"omp"},
	WinNames:      []string{"omp.cmd", "omp.exe", "omp"},
	UnixPaths:     []string{"/usr/local/bin/omp", "/opt/homebrew/bin/omp"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("omp", []string{".omp", "bin", "omp"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "omp.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "omp.exe"}},
	},
}

func ResolveOMPBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, ompBinarySpec)
}

func (p *Plugin) ompBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := ResolveOMPBinary(ctx)
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
