package copilot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "copilot"

var copilotUnixPaths = []string{
	"/usr/local/bin/copilot",
	"/opt/homebrew/bin/copilot",
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
		Name:        "GitHub Copilot",
		Description: "Run GitHub Copilot CLI worker sessions.",
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
				Description: "Model override passed to `copilot --model` (e.g. claude-sonnet-4.5).",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.copilotBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = append(cmd, binary, "--no-auto-update")
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	if agentName := copilotAgentName(cfg.SessionID, cfg.SystemPrompt, cfg.SystemPromptFile); agentName != "" {
		cmd = append(cmd, "--agent="+agentName)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--interactive", cfg.Prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.copilotBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = append(cmd, binary, "--no-auto-update")
	appendApprovalFlags(&cmd, cfg.Permissions)
	if agentName := copilotAgentName(cfg.Session.ID, cfg.SystemPrompt, cfg.SystemPromptFile); agentName != "" {
		cmd = append(cmd, "--agent="+agentName)
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

func ResolveCopilotBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if managed, ok := binaryutil.ManagedBinary("copilot"); ok {
		if native := copilotManagedNativeBinary(managed); native != "" {
			return native, nil
		}
		return managed, nil
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"copilot.cmd", "copilot.exe", "copilot"} {
			if path := lookCopilotOnPath(name); path != "" {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "copilot.cmd"),
				filepath.Join(appData, "npm", "copilot.exe"),
			)
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".copilot", "bin", "copilot.exe"))
		}
		candidates = append(candidates, binaryutil.WindowsPackageManagerBinCandidates("copilot")...)
		for _, candidate := range candidates {
			if hookutil.IsExecutableFile(candidate) {
				return candidate, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		return "", fmt.Errorf("copilot: %w", ports.ErrAgentBinaryNotFound)
	}

	if path := lookCopilotOnPath("copilot"); path != "" {
		if native := copilotNativeBinaryForLoader(path); native != "" {
			return native, nil
		}
		return path, nil
	}

	candidates := append([]string(nil), copilotUnixPaths...)
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".npm-global", "bin", "copilot"),
			filepath.Join(home, ".npm", "bin", "copilot"),
			filepath.Join(home, ".local", "bin", "copilot"),
			filepath.Join(home, ".copilot", "bin", "copilot"),
		)
		candidates = append(candidates, binaryutil.UnixPackageManagerBinCandidates(home, "copilot")...)
		nodeManagerCandidates, err := binaryutil.UnixNodeManagerBinCandidates(ctx, home, "copilot")
		if err != nil {
			return "", err
		}
		candidates = append(candidates, nodeManagerCandidates...)
	}

	for _, candidate := range candidates {
		if hookutil.IsExecutableFile(candidate) && !isVSCodeCopilotLauncher(candidate) {
			if native := copilotNativeBinaryForLoader(candidate); native != "" {
				return native, nil
			}
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("copilot: %w", ports.ErrAgentBinaryNotFound)
}

func copilotManagedNativeBinary(launcher string) string {
	current, ok := binaryutil.ManagedCurrentDir(launcher, "copilot")
	if !ok {
		return ""
	}
	native := copilotNativeBinaryForLoader(launcher)
	release, err := filepath.EvalSymlinks(current)
	if native == "" || err != nil {
		return ""
	}
	rel, err := filepath.Rel(release, native)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.Join(current, rel)
}

const vsCodeCopilotLauncherScript = "copilotCLIShim.js"

func isVSCodeCopilotLauncher(path string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(path), vsCodeCopilotLauncherScript))
	return err == nil
}

func lookCopilotOnPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil || path == "" {
		return ""
	}
	if !isVSCodeCopilotLauncher(path) {
		return path
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate, err := exec.LookPath(filepath.Join(dir, name))
		if err == nil && candidate != "" && !isVSCodeCopilotLauncher(candidate) {
			return candidate
		}
	}
	return ""
}

func copilotNativeBinaryForLoader(path string) string {
	if path == "" || runtime.GOOS == "windows" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Base(resolved) != "npm-loader.js" {
		return ""
	}
	for _, name := range copilotNativeNames(runtime.GOOS, runtime.GOARCH) {
		native := filepath.Join(filepath.Dir(resolved), "node_modules", ".bin", name)
		if hookutil.IsExecutableFile(native) {
			return native
		}
	}
	return ""
}

func copilotNativeNames(goos, goarch string) []string {
	if goarch == "amd64" {
		goarch = "x64"
	}
	names := []string{"copilot-" + goos + "-" + goarch}
	if goos == "linux" {
		names = append(names, "copilot-linuxmusl-"+goarch)
	}
	return names
}

func (p *Plugin) copilotBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveCopilotBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func copilotSystemPromptText(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" {
		return strings.TrimRight(inline, "\n"), nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("copilot: read system prompt file: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func AgentProfileName(sessionID, systemPrompt, systemPromptFile string) string {
	return copilotAgentName(sessionID, systemPrompt, systemPromptFile)
}

func copilotAgentName(sessionID, inlinePrompt, promptFile string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	if strings.TrimSpace(inlinePrompt) == "" && strings.TrimSpace(promptFile) == "" {
		return ""
	}
	return "ao-" + copilotAgentNameReplacer.Replace(strings.TrimSpace(sessionID))
}

var copilotAgentNameReplacer = strings.NewReplacer(
	"/", "-",
	"\\", "-",
	" ", "-",
	"_", "-",
	".", "-",
	":", "-",
)

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	*cmd = append(*cmd, ApprovalArgs(permissions)...)
}

func ApprovalArgs(permissions ports.PermissionMode) []string {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAcceptEdits:
		return []string{"--allow-tool", "write"}
	case ports.PermissionModeAuto:
		return []string{"--allow-all-tools"}
	case ports.PermissionModeBypassPermissions:
		return []string{"--allow-all"}
	default:
		return nil
	}
}

func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if trimmed := strings.TrimSpace(cfg.Model); trimmed != "" {
		*cmd = append(*cmd, "--model", trimmed)
	}
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
