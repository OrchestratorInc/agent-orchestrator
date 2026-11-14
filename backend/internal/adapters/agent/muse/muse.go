package muse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const adapterID = "muse"

const museDeveloperPromptEnvVar = "TBH_EVAL_APPEND_DEVELOPER_PROMPT"

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
var _ ports.ContinuousTerminalActivityDetector = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Muse Code",
		Description: "Run Muse Code CLI worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `muse --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := p.museBinary(ctx)
	if err != nil {
		return nil, err
	}
	systemPrompt, err := museSystemPromptText(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, fmt.Errorf("muse.GetLaunchCommand: %w", err)
	}

	cmd := make([]string, 0, 11)
	env := make([]string, 0, 2)
	if systemPrompt != "" {
		env = append(env, museDeveloperPromptEnvVar+"="+systemPrompt)
	}
	if cfg.DataDir != "" || cfg.SessionID != "" {
		hooksPath, pathErr := museManagedHooksPath(cfg.DataDir, cfg.SessionID)
		if pathErr != nil {
			return nil, fmt.Errorf("muse.GetLaunchCommand: %w", pathErr)
		}
		env = append(env, museManagedHooksEnvVar+"="+hooksPath)
	}
	if len(env) > 0 {
		cmd = append(cmd, "env")
		cmd = append(cmd, env...)
	}
	cmd = append(cmd, binary, "--trust-workspace")
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
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

	binary, err := p.museBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	systemPrompt, err := museSystemPromptText(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, false, fmt.Errorf("muse.GetRestoreCommand: %w", err)
	}

	cmd = make([]string, 0, 12)
	env := make([]string, 0, 2)
	if systemPrompt != "" {
		env = append(env, museDeveloperPromptEnvVar+"="+systemPrompt)
	}
	if cfg.DataDir != "" || cfg.Session.ID != "" {
		hooksPath, pathErr := museManagedHooksPath(cfg.DataDir, cfg.Session.ID)
		if pathErr != nil {
			return nil, false, fmt.Errorf("muse.GetRestoreCommand: %w", pathErr)
		}
		env = append(env, museManagedHooksEnvVar+"="+hooksPath)
	}
	if len(env) > 0 {
		cmd = append(cmd, "env")
		cmd = append(cmd, env...)
	}

	cmd = append(cmd, binary, "--trust-workspace")
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	cmd = append(cmd, "resume", agentSessionID)
	if cfg.Prompt != "" {
		cmd = append(cmd, cfg.Prompt)
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

func appendApprovalFlags(cmd *[]string, mode ports.PermissionMode) {
	switch mode {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		*cmd = append(*cmd, "--approval-mode", "never")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--yolo")
	}
}

var museBinarySpec = binaryutil.BinarySpec{
	Label:    "muse",
	Names:    []string{"muse"},
	WinNames: []string{"muse.exe", "muse.cmd", "muse"},
	UnixPaths: []string{
		"/usr/local/bin/muse",
		"/opt/homebrew/bin/muse",
	},
	UnixHomePaths: [][]string{
		{".local", "bin", "muse"},
	},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "muse.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "muse.exe"}},
	},
}

func ResolveMuseBinary(ctx context.Context) (string, error) {
	return resolveMuseBinary(ctx, museBinarySpec)
}

func resolveMuseBinary(ctx context.Context, spec binaryutil.BinarySpec) (string, error) {
	first, err := binaryutil.ResolveBinary(ctx, spec)
	if err != nil {
		return "", err
	}

	candidates := append([]string{first}, museCanonicalBinaryCandidates(spec)...)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if isOfficialMuseBinary(ctx, candidate) {
			return candidate, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: %w", spec.Label, ports.ErrAgentBinaryNotFound)
}

func isOfficialMuseBinary(ctx context.Context, binary string) bool {
	cmd := aoprocess.CommandContext(ctx, binary, "--version")
	cmd.Env = append(os.Environ(), "MUSE_NO_AUTO_UPDATE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Muse Code ")
}

func museCanonicalBinaryCandidates(spec binaryutil.BinarySpec) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	candidates := append([]string(nil), spec.UnixPaths...)
	home, err := os.UserHomeDir()
	if err != nil {
		return candidates
	}
	for _, parts := range spec.UnixHomePaths {
		candidates = append(candidates, filepath.Join(append([]string{home}, parts...)...))
	}
	return candidates
}

func (p *Plugin) museBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := ResolveMuseBinary(ctx)
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
