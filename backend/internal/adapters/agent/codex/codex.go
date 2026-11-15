package codex

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) EmitsSubmitActivity() bool { return true }

func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

func (p *Plugin) EmitsBlockedActivity() bool { return false }

func (p *Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

func (p *Plugin) SteersActiveTurn() bool { return true }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.ActiveTurnSteerer = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoff = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
var _ ports.TerminalActivityDetector = (*Plugin)(nil)
var _ ports.EmptyComposerDetector = (*Plugin)(nil)
var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)

func (p *Plugin) ComposerIsEmpty(output string) bool {
	return p.InspectTerminalSurface(output).Composer == ports.TerminalComposerEmpty
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "codex",
		Name:        "Codex",
		Description: "Run Codex worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelEffortConfigSpec(ctx,
		"Model override passed to `codex --model`.",
		"Reasoning effort override passed to `codex -c model_reasoning_effort`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.codexBinary(ctx)
	if err != nil {
		return nil, err
	}

	var providerArgs []string
	if err := appendSessionHookFlags(&providerArgs); err != nil {
		return nil, err
	}
	appendTerminalCompatibilityFlags(&providerArgs)
	appendReasoningEffortFlag(&providerArgs, cfg.Config.Effort)
	appendFastTierFlag(&providerArgs, cfg.Config.Launch)
	return agentruntime.BuildLaunchCommand(agentruntime.LaunchConfig{
		Harness:          agentruntime.HarnessCodex,
		Binary:           binary,
		WorkspacePath:    cfg.WorkspacePath,
		Model:            cfg.Config.Model,
		Prompt:           cfg.Prompt,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
		Permission:       agentruntime.PermissionPolicy(cfg.Permissions),
		ProviderArgs:     providerArgs,
	})
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, ok := agentruntime.RestoreIdentity(
		agentruntime.HarnessCodex,
		cfg.Session.ID,
		cfg.Session.Metadata,
	); !ok {
		return nil, false, nil
	}
	binary, err := p.codexBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	var providerArgs []string
	if err := appendSessionHookFlags(&providerArgs); err != nil {
		return nil, false, err
	}
	appendTerminalCompatibilityFlags(&providerArgs)
	appendReasoningEffortFlag(&providerArgs, cfg.Config.Effort)
	appendFastTierFlag(&providerArgs, cfg.Config.Launch)
	return agentruntime.BuildRestoreCommand(agentruntime.RestoreConfig{
		Harness:          agentruntime.HarnessCodex,
		Binary:           binary,
		SessionID:        cfg.Session.ID,
		Metadata:         cfg.Session.Metadata,
		WorkspacePath:    cfg.Session.WorkspacePath,
		Model:            cfg.Config.Model,
		Prompt:           cfg.Prompt,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
		Permission:       agentruntime.PermissionPolicy(cfg.Permissions),
		ProviderArgs:     providerArgs,
	})
}

func appendFastTierFlag(args *[]string, launch domain.AgentLaunchOptions) {
	switch {
	case launch.FastMode:
		*args = append(*args, "-c", "service_tier="+codexTOMLConfigString("priority"))
	case launch.FastModeOff:
		*args = append(*args, "-c", "service_tier="+codexTOMLConfigString("default"))
	}
}

func appendReasoningEffortFlag(args *[]string, effort string) {
	if effort = strings.TrimSpace(effort); effort != "" {
		*args = append(*args, "-c", "model_reasoning_effort="+codexTOMLConfigString(effort))
	}
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func (p *Plugin) NativeConversationID(
	ctx context.Context,
	session ports.SessionRef,
	currentMode domain.SessionMode,
	providerConversationID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if currentMode == domain.SessionModeChat {
		id := strings.TrimSpace(providerConversationID)
		return id, id != "", nil
	}
	id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	return id, id != "", nil
}

func (p *Plugin) NativeConversationExists(
	ctx context.Context,
	_ ports.SessionRef,
	nativeConversationID string,
	env map[string]string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, valid := canonicalCodexThreadID(nativeConversationID)
	if !valid {
		return false, nil
	}
	codexHome := strings.TrimSpace(env["CODEX_HOME"])
	if codexHome == "" {
		codexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("codex: resolve rollout root: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}

	found := false
	sessionsDir := filepath.Join(codexHome, "sessions")
	if resolved, err := filepath.EvalSymlinks(sessionsDir); err == nil {
		sessionsDir = resolved
	}
	err := filepath.WalkDir(sessionsDir, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !codexRolloutNameMatches(entry.Name(), id) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("codex: inspect rollout root %s: %w", sessionsDir, err)
	}
	return found, nil
}

func canonicalCodexThreadID(value string) (string, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func codexRolloutNameMatches(name, nativeConversationID string) bool {
	if !strings.HasPrefix(name, "rollout-") {
		return false
	}
	suffix := "-" + nativeConversationID + ".jsonl"
	return strings.HasSuffix(name, suffix) || strings.HasSuffix(name, suffix+".zst")
}

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.codexBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if provider, ok := customModelProvider(); ok {
		if provider.EnvKey != "" && authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, provider.EnvKey) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
		return ports.AgentAuthStatusUnknown, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	started := time.Now()
	cmd := aoprocess.CommandContext(probeCtx, binary, "login", "status")
	cmd.Env = authprobe.Environ(ctx, agentlaunch.ProcessEnvForLaunchBinary(probeCtx, binary))
	out, err := cmd.CombinedOutput()
	status, recognized := codexAuthStatusFromOutput(out)
	if !recognized {
		status = ports.AgentAuthStatusUnknown
	}
	ports.RecordAuthDiagnostic(ctx, authprobe.CommandStep(binary, []string{"login", "status"}, status, err, probeCtx.Err(), started))
	if probeCtx.Err() != nil {
		return ports.AgentAuthStatusUnknown, probeCtx.Err()
	}
	if recognized {
		if status == ports.AgentAuthStatusAuthorized {
			evidence := domain.CommandSource(binary, "login", "status").WithMethod(codexLoginMethod(out))
			if evidence.Method == domain.AgentAccountMethodAccount {
				evidence.Email, evidence.Name = codexLoginIdentity(ctx)
			}
			ports.RecordAuthEvidence(ctx, evidence)
		}
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func codexLoginIdentity(ctx context.Context) (email, name string) {
	home := codexHomeDir()
	if home == "" {
		return "", ""
	}
	return authprobe.CodexAuthFileIdentity(ctx, filepath.Join(home, "auth.json"))
}

func codexHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		return ""
	}
	return filepath.Join(userHome, ".codex")
}

type codexModelProvider struct {
	EnvKey             string `toml:"env_key"`
	RequiresOpenAIAuth bool   `toml:"requires_openai_auth"`
}

func UsesCustomModelProvider() bool {
	_, ok := customModelProvider()
	return ok
}

func customModelProvider() (codexModelProvider, bool) {
	home := codexHomeDir()
	if home == "" {
		return codexModelProvider{}, false
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return codexModelProvider{}, false
	}
	var config struct {
		ModelProvider string `toml:"model_provider"`
		Profile       string `toml:"profile"`
		Profiles      map[string]struct {
			ModelProvider string `toml:"model_provider"`
		} `toml:"profiles"`
		ModelProviders map[string]codexModelProvider `toml:"model_providers"`
	}
	if toml.Unmarshal(data, &config) != nil {
		return codexModelProvider{}, false
	}
	name := config.ModelProvider
	if profile := config.Profiles[config.Profile].ModelProvider; profile != "" {
		name = profile
	}
	provider := config.ModelProviders[name]
	if name == "" || name == "openai" || provider.RequiresOpenAIAuth {
		return codexModelProvider{}, false
	}
	return provider, true
}

func codexLoginMethod(out []byte) domain.AgentAccountMethod {
	text := strings.ToLower(string(out))
	switch {
	case strings.Contains(text, "api key"):
		return domain.AgentAccountMethodAPIKey
	case strings.Contains(text, "chatgpt"):
		return domain.AgentAccountMethodAccount
	default:
		return ""
	}
}

func codexAuthStatusFromOutput(out []byte) (ports.AgentAuthStatus, bool) {
	text := strings.ToLower(string(out))
	if strings.Contains(text, "not logged in") || strings.Contains(text, "logged out") {
		return ports.AgentAuthStatusUnauthorized, true
	}
	if strings.Contains(text, "logged in") {
		return ports.AgentAuthStatusAuthorized, true
	}
	return ports.AgentAuthStatusUnknown, false
}

func ResolveCodexBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if managed, ok := binaryutil.ManagedBinary("codex"); ok {
		return resolveNativeWindowsCodex(managed, runtime.GOOS), nil
	}

	if runtime.GOOS == "windows" {
		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			shim := filepath.Join(appData, "npm", "codex.cmd")
			candidates = append(candidates, windowsNativeCodexCandidatesForShim(shim)...)
			candidates = append(candidates,
				filepath.Join(appData, "npm", "codex.exe"),
				shim,
			)
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".cargo", "bin", "codex.exe"))
		}
		candidates = append(candidates, binaryutil.WindowsPackageManagerBinCandidates("codex")...)
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return resolveNativeWindowsCodex(candidate, runtime.GOOS), nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		for _, name := range []string{"codex.cmd", "codex", "codex.exe"} {
			path, err := exec.LookPath(name)
			if err == nil && path != "" {
				if isWindowsAppsCodexExecutable(path) {
					continue
				}
				return resolveNativeWindowsCodex(path, runtime.GOOS), nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		return "", fmt.Errorf("codex: %w", ports.ErrAgentBinaryNotFound)
	}

	if path, err := exec.LookPath("codex"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/codex",
		"/opt/homebrew/bin/codex",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".npm-global", "bin", "codex"),
			filepath.Join(home, ".npm", "bin", "codex"),
			filepath.Join(home, ".local", "bin", "codex"),
			filepath.Join(home, ".cargo", "bin", "codex"),
		)
		candidates = append(candidates, binaryutil.UnixPackageManagerBinCandidates(home, "codex")...)
		nodeManagerCandidates, err := binaryutil.UnixNodeManagerBinCandidates(ctx, home, "codex")
		if err != nil {
			return "", err
		}
		candidates = append(candidates, nodeManagerCandidates...)
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("codex: %w", ports.ErrAgentBinaryNotFound)
}

func resolveNativeWindowsCodex(path, goos string) string {
	if goos != "windows" || !strings.EqualFold(filepath.Ext(path), ".cmd") {
		return path
	}
	for _, candidate := range windowsNativeCodexCandidatesForShim(path) {
		if fileExists(candidate) {
			return candidate
		}
	}
	return path
}

func windowsNativeCodexCandidatesForShim(shim string) []string {
	candidates := windowsNativeCodexCandidatesInPrefix(filepath.Dir(shim))
	if current, ok := binaryutil.ManagedCurrentDir(shim, "codex"); ok {
		candidates = append(windowsNativeCodexCandidatesInPrefix(current), candidates...)
	}
	return candidates
}

func windowsNativeCodexCandidatesInPrefix(dir string) []string {
	return []string{
		filepath.Join(dir, "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe"),
		filepath.Join(dir, "node_modules", "@openai", "codex", "bin", "codex.exe"),
	}
}

func isWindowsAppsCodexExecutable(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	clean := strings.ToLower(filepath.Clean(path))
	base := filepath.Base(clean)
	return (base == "codex.exe" || base == "codex") &&
		strings.Contains(clean, string(filepath.Separator)+"windowsapps"+string(filepath.Separator)+"openai.codex_")
}

func (p *Plugin) codexBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveCodexBinary(ctx)
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

func DoctorLaunchProbes() [][]string {
	flagProbe := make([]string, 0, 2)
	appendHookTrustBypassFlag(&flagProbe)
	flagProbe = append(flagProbe, "--version")

	overrideProbe := []string{"features", "list"}
	appendNoUpdateCheckFlag(&overrideProbe)
	appendHideRateLimitNudgeFlag(&overrideProbe)
	if err := appendSessionHookFlags(&overrideProbe); err != nil {
		appendSessionHookFlagsForExecutable(&overrideProbe, "ao")
	}
	appendWorkspaceTrustFlag(&overrideProbe, os.TempDir())
	return [][]string{flagProbe, overrideProbe}
}

func appendNoUpdateCheckFlag(cmd *[]string) {
	*cmd = append(*cmd, "-c", "check_for_update_on_startup=false")
}

func appendHideRateLimitNudgeFlag(cmd *[]string) {
	*cmd = append(*cmd, "-c", "notice.hide_rate_limit_model_nudge=true")
}

func appendHookTrustBypassFlag(cmd *[]string) {
	*cmd = append(*cmd, "--dangerously-bypass-hook-trust")
}

func appendTerminalCompatibilityFlags(cmd *[]string) {
	if runtime.GOOS == "windows" {
		*cmd = append(*cmd, "--no-alt-screen")
	}
}

var fileExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
