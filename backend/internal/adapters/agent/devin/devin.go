package devin

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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var devinBinarySpec = binaryutil.BinarySpec{
	Label:         "devin",
	Names:         []string{"devin"},
	WinNames:      []string{"devin.cmd", "devin.exe", "devin"},
	UnixPaths:     []string{"/usr/local/bin/devin", "/opt/homebrew/bin/devin"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("devin", []string{".devin", "bin", "devin"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinHome, Parts: []string{".devin", "bin", "devin.exe"}},
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
		ID:          "devin",
		Name:        "Devin",
		Description: "Run Cognition Devin for Terminal worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `devin --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.devinBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if prompt := strings.TrimSpace(cfg.Prompt); prompt != "" {
		cmd = append(cmd, "--", prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

func (p *Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.WorkspacePath == "" {
		return nil
	}
	nativePath, err := devinTrustedWorkspacesPath()
	if err != nil {
		return err
	}
	if err := ensureDevinNativeWorkspaceTrusted(nativePath, cfg.WorkspacePath); err != nil {
		return err
	}
	cfgPath, err := devinClaudeConfigPath()
	if err != nil {
		return err
	}
	return ensureDevinWorkspaceTrusted(cfgPath, cfg.WorkspacePath)
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return devinHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.devinBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 5)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
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

func ResolveDevinBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, devinBinarySpec)
}

func (p *Plugin) devinBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveDevinBinary(ctx)
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
		*cmd = append(*cmd, "--permission-mode", "accept-edits")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--permission-mode", "auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--permission-mode", "dangerous")
	}
}

func devinClaudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("devin: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

func devinTrustedWorkspacesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("devin: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "devin", "cli", "trusted_workspaces.json"), nil
}

var devinTrustMu sync.Mutex

type devinTrustedWorkspaces struct {
	TrustedPaths []string `json:"trusted_paths"`
}

func ensureDevinNativeWorkspaceTrusted(configPath, workspacePath string) error {
	devinTrustMu.Lock()
	defer devinTrustMu.Unlock()

	root := devinTrustedWorkspaces{}
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return fmt.Errorf("devin: parse %s: %w", configPath, err)
			}
		}
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("devin: read %s: %w", configPath, err)
	}

	for _, path := range root.TrustedPaths {
		if path == workspacePath {
			return nil
		}
	}
	root.TrustedPaths = append(root.TrustedPaths, workspacePath)

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("devin: encode %s: %w", configPath, err)
	}
	return writeDevinTrustFile(configPath, out)
}

func ensureDevinWorkspaceTrusted(configPath, workspacePath string) error {
	devinTrustMu.Lock()
	defer devinTrustMu.Unlock()

	root := map[string]any{}
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return fmt.Errorf("devin: parse %s: %w", configPath, err)
			}
		}
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("devin: read %s: %w", configPath, err)
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}

	entry, _ := projects[workspacePath].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[workspacePath] = entry
	}

	if trusted, ok := entry["hasTrustDialogAccepted"].(bool); ok && trusted {
		return nil
	}
	entry["hasTrustDialogAccepted"] = true

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("devin: encode %s: %w", configPath, err)
	}

	return writeDevinTrustFile(configPath, out)
}

func writeDevinTrustFile(configPath string, out []byte) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("devin: create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return fmt.Errorf("devin: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("devin: write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("devin: close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("devin: replace config: %w", err)
	}
	return nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
