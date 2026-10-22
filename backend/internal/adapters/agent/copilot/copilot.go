// Package copilot implements the GitHub Copilot CLI agent adapter: launching new
// headless sessions, resuming hook-tracked sessions, installing workspace-local
// hooks, and reading hook-derived session info.
//
// This adapter targets the standalone agentic GitHub Copilot CLI (binary
// "copilot", installed via npm "@github/copilot"), NOT the older `gh copilot`
// suggest/explain extension.
//
// Launch runs the CLI in interactive mode so AO can keep a durable terminal
// pane attached to the session. When AO has an initial task, it uses Copilot's
// `--interactive <prompt>` mode so the task executes immediately instead of
// waiting in the terminal input buffer. Permission modes map onto the CLI's allow
// flags (`--allow-tool`, `--allow-all-tools`, `--allow-all`).
// Restore continues an existing session via `--resume <agentSessionId>`; the
// native session id (a UUID under ~/.copilot/session-state/) is captured by the
// SessionStart hook AO installs (see hooks.go).
//
// AO-managed sessions derive native session identity and display metadata from
// Copilot hooks instead of transcript/cache scans.
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

// Plugin is the GitHub Copilot CLI agent adapter. It is safe for concurrent use;
// the binary path is resolved once and cached under binaryMu.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Copilot adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
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

// GetConfigSpec reports the per-project agent config keys the GitHub Copilot
// CLI understands: a model override passed via --model.
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

// GetLaunchCommand builds the argv to start a new interactive Copilot session:
//
//	copilot [permission flags] [--agent ao-<session>] [--interactive <prompt>]
//
// `--interactive <prompt>` keeps the durable Copilot terminal session open while
// automatically submitting the initial task. `-p` is deliberately avoided
// because it runs Copilot in programmatic mode and exits when done. Copilot CLI
// does not expose a system-prompt flag, so AO installs a per-session custom
// agent profile in GetAgentHooks and selects it here.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.copilotBinary(ctx)
	if err != nil {
		return nil, err
	}

	// Keep the release immutable while AO owns refresh and retention.
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

// GetPromptDeliveryStrategy reports that Copilot receives its prompt in the
// launch command. This uses `--interactive <prompt>`, not `-p`, so Copilot starts
// executing immediately while keeping the interactive terminal session alive.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing Copilot
// session: `copilot [permission flags] --resume <agentSessionId>`.
// ok is false when the hook-derived native session id has not landed yet, so
// callers can fall back to fresh launch behavior.
//
// ports.RestoreConfig carries no Prompt field, so resume is issued without a new
// `-p`; the manager re-sends the prompt through its own delivery path when one is
// needed.
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

	// Keep the release immutable while AO owns refresh and retention.
	cmd = append(cmd, binary, "--no-auto-update")
	appendApprovalFlags(&cmd, cfg.Permissions)
	// Deliberately does not forward cfg.Config.Model: --model + --resume
	// composition is unverified against a real copilot install (see #2895
	// and appendModelFlag). Pinned by
	// TestGetRestoreCommandDoesNotForwardModelOverride.
	if agentName := copilotAgentName(cfg.Session.ID, cfg.SystemPrompt, cfg.SystemPromptFile); agentName != "" {
		cmd = append(cmd, "--agent="+agentName)
	}
	cmd = append(cmd, "--resume", agentSessionID)
	return cmd, true, nil
}

// SessionInfo surfaces Copilot hook-derived metadata. Metadata is intentionally
// nil for Copilot: callers get the normalized fields directly.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// ResolveCopilotBinary returns the path to the copilot binary on this machine,
// searching PATH then a handful of well-known install locations (npm global,
// Homebrew). When the resolved path is the npm-loader shim, the platform-native
// binary is returned instead. This resolver stays hand-rolled (rather than
// binaryutil.ResolveBinary) because of that native-loader indirection.
//
// VS Code's launcher is never an answer: see isVSCodeCopilotLauncher.
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

// copilotManagedNativeBinary is the native binary behind a managed npm copy's
// loader launcher, reached through current so the path still follows updates.
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

// vsCodeCopilotLauncherScript sits beside the `copilot` launcher VS Code's
// Copilot Chat extension puts on its terminals' PATH.
const vsCodeCopilotLauncherScript = "copilotCLIShim.js"

// isVSCodeCopilotLauncher reports whether path is VS Code's launcher, which only
// forwards to a `copilot` elsewhere on PATH and, with none, stops at "Install
// GitHub Copilot CLI? [y/N]". It never shows that the CLI is installed.
func isVSCodeCopilotLauncher(path string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(path), vsCodeCopilotLauncherScript))
	return err == nil
}

// lookCopilotOnPath is exec.LookPath that passes over VS Code's launcher and
// keeps searching the rest of PATH for the CLI itself.
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

// copilotNativeNames are the npm platform binaries the loader may run on
// goos/goarch. npm names amd64 x64, and musl Linux has its own packages.
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

// AgentProfileName is the per-session custom-agent profile AO installs for a
// session with standing instructions, or "" when there are none. The terminal
// launch selects it with --agent; Chat selects the same profile over ACP.
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

// appendApprovalFlags maps AO's 4 permission modes onto Copilot CLI approval
// flags (https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-programmatic-reference):
//
//	default            -> no flag (defer to ~/.copilot config / per-tool prompts)
//	accept-edits       -> --allow-tool 'write' (auto-approve file edits only)
//	auto               -> --allow-all-tools (auto-approve every tool, still scoped paths/urls)
//	bypass-permissions -> --allow-all (full bypass: tools, paths, urls)
func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	*cmd = append(*cmd, ApprovalArgs(permissions)...)
}

// ApprovalArgs returns the Copilot CLI approval flags for one AO permission
// mode, so the terminal and Chat launches share one answer to what each mode
// allows.
func ApprovalArgs(permissions ports.PermissionMode) []string {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAcceptEdits:
		return []string{"--allow-tool", "write"}
	case ports.PermissionModeAuto:
		return []string{"--allow-all-tools"}
	case ports.PermissionModeBypassPermissions:
		return []string{"--allow-all"}
	default:
		// No flag: defer to the user's ~/.copilot config / interactive prompts.
		return nil
	}
}

// appendModelFlag appends `--model <trimmed>` when cfg.Model is set,
// mirroring the Codex adapter's pattern (see #2869) and taking the full
// ports.AgentConfig for consistency with the sibling adapters. A blank or
// whitespace-only value is omitted so Copilot falls back to its own default
// resolution (COPILOT_MODEL env, or its configured default) exactly as an
// unconfigured launch would.
//
// Restore-path note: this is intentionally NOT called from
// GetRestoreCommand. An attempt to verify `--model` + `--resume`
// composition against a real copilot install was inconclusive: the test
// account is on the Copilot Free plan, which only grants access to "auto"
// regardless of --model, on both launch and resume. That makes the plan
// itself a confound, not evidence either way about whether --resume
// honors --model. Left as a fast-follow pending verification from an
// account with paid-plan model access (see #2895).
func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if trimmed := strings.TrimSpace(cfg.Model); trimmed != "" {
		*cmd = append(*cmd, "--model", trimmed)
	}
}

// InvalidateBinaryResolution forgets the cached executable path, so the next
// launch resolves it again: after AO installs a managed copy, for example.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
