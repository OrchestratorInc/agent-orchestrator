package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

const (
	adapterID = "claude-code"
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

func (p *Plugin) EmitsBlockedActivity() bool { return true }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)
var _ ports.EmptyComposerDetector = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoff = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)

func (p *Plugin) ComposerIsEmpty(output string) bool {
	return p.InspectTerminalSurface(output).Composer == ports.TerminalComposerEmpty
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Claude Code",
		Description: "Run Claude Code worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

var permissionConfigEnum = []string{
	string(ports.PermissionModeDefault),
	string(ports.PermissionModeAcceptEdits),
	string(ports.PermissionModeAuto),
	string(ports.PermissionModeBypassPermissions),
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
				Description: "Model override passed to `claude --model` (e.g. claude-opus-4-5).",
			},
			{
				Key:  "effort",
				Type: ports.ConfigFieldString,
				Description: "Reasoning level passed to `claude --effort`. Valid levels are " +
					"per-model and come from the provider's own catalog, so this is not a fixed enum.",
			},
			{
				Key:         "permissions",
				Type:        ports.ConfigFieldEnum,
				Description: "Starting permission mode.",
				Enum:        permissionConfigEnum,
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := cfg.Config.Validate(); err != nil {
		return nil, fmt.Errorf("claude-code: %w", err)
	}

	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return nil, err
	}

	permissions := cfg.Permissions
	if permissions == "" {
		permissions = cfg.Config.Permissions
	}
	nativeSessionID, err := p.freshLaunchNativeSessionID(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return agentruntime.BuildLaunchCommand(agentruntime.LaunchConfig{
		Harness:          agentruntime.HarnessClaudeCode,
		Binary:           binary,
		SessionID:        cfg.SessionID,
		NativeSessionID:  nativeSessionID,
		ProviderArgs:     claudeLaunchSettingsArgs(cfg.Config.Launch, agentruntime.PermissionPolicy(permissions)),
		Model:            cfg.Config.Model,
		Effort:           cfg.Config.Effort,
		Prompt:           cfg.Prompt,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
		Permission:       agentruntime.PermissionPolicy(permissions),
		AllowedTools:     cfg.AllowedTools,
		DisallowedTools:  cfg.DisallowedTools,
	})
}

func claudeLaunchSettingsArgs(options domain.AgentLaunchOptions, policy agentruntime.PermissionPolicy) []string {
	settings := autoDefaultNudgeGuardSettings(policy)
	if settings == nil {
		settings = map[string]any{}
	}
	if options.Ultracode {
		settings["ultracode"] = true
	}
	if tokens := domain.AutoCompactTokens[options.AutoCompactWindow]; tokens > 0 {
		settings["autoCompactEnabled"] = true
		settings["autoCompactWindow"] = tokens
	}
	if len(settings) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(settings)
	return []string{"--settings", string(encoded)}
}

func (p *Plugin) freshLaunchNativeSessionID(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if cfg.NativeSessionID != "" || cfg.SessionID == "" {
		return cfg.NativeSessionID, nil
	}
	taken, err := p.NativeConversationExists(ctx, ports.SessionRef{}, claudeSessionUUID(cfg.SessionID), nil)
	if err != nil {
		return "", err
	}
	if taken {
		return p.NewNativeSessionID(), nil
	}
	return "", nil
}

func (p *Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.WorkspacePath == "" {
		return nil
	}
	settingsPath, cfgPath, err := claudeLaunchConfigPaths(cfg.Env)
	if err != nil {
		return err
	}
	permissions := cfg.Permissions
	if permissions == "" {
		permissions = cfg.Config.Permissions
	}
	markNudgeSeen := len(autoDefaultNudgeGuardArgs(agentruntime.PermissionPolicy(permissions))) == 0 &&
		userSettingsOfferAutoDefaultNudge(settingsPath)
	return ensureClaudeLaunchConfigForOS(cfgPath, cfg.WorkspacePath, markNudgeSeen, runtime.GOOS)
}

func claudeLaunchConfigPaths(env map[string]string) (settingsPath, globalConfigPath string, err error) {
	configDir, ok := env[claudeConfigDirEnv]
	if !ok {
		configDir = os.Getenv(claudeConfigDirEnv)
	}
	configDir = strings.TrimSpace(configDir)
	var globalDir string
	if configDir != "" {
		configDir = filepath.Clean(configDir)
		globalDir = configDir
	} else {
		home := strings.TrimSpace(env["HOME"])
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				return "", "", fmt.Errorf("claude-code: resolve home directory: %w", err)
			}
		}
		configDir, globalDir = filepath.Join(home, ".claude"), home
	}
	globalConfigPath = filepath.Join(globalDir, ".claude.json")
	legacy := filepath.Join(configDir, ".config.json")
	if info, statErr := os.Stat(legacy); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
		globalConfigPath = legacy
	}
	return filepath.Join(configDir, "settings.json"), globalConfigPath, nil
}

func autoDefaultNudgeGuardArgs(policy agentruntime.PermissionPolicy) []string {
	settings := autoDefaultNudgeGuardSettings(policy)
	if settings == nil {
		return nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil
	}
	return []string{"--settings", string(encoded)}
}

func autoDefaultNudgeGuardSettings(policy agentruntime.PermissionPolicy) map[string]any {
	flag := agentruntime.ClaudePermissionArgs(policy)
	if len(flag) != 2 {
		return nil
	}
	return map[string]any{"permissions": map[string]any{"defaultMode": flag[1]}}
}

func userSettingsOfferAutoDefaultNudge(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath) //nolint:gosec // the user's own Claude settings path
	if err != nil {
		return false
	}
	var settings struct {
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}
	mode := strings.TrimSpace(settings.Permissions.DefaultMode)
	return mode != "" && mode != "auto"
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, ok := agentruntime.RestoreIdentity(
		agentruntime.HarnessClaudeCode,
		cfg.Session.ID,
		cfg.Session.Metadata,
	); !ok {
		return nil, false, nil
	}

	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	return agentruntime.BuildRestoreCommand(agentruntime.RestoreConfig{
		Harness:          agentruntime.HarnessClaudeCode,
		Binary:           binary,
		SessionID:        cfg.Session.ID,
		Metadata:         cfg.Session.Metadata,
		ProviderArgs:     claudeLaunchSettingsArgs(cfg.Config.Launch, agentruntime.PermissionPolicy(cfg.Permissions)),
		Model:            cfg.Config.Model,
		Effort:           cfg.Config.Effort,
		Prompt:           cfg.Prompt,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
		Permission:       agentruntime.PermissionPolicy(cfg.Permissions),
		AllowedTools:     cfg.AllowedTools,
		DisallowedTools:  cfg.DisallowedTools,
	})
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
	if id == "" && session.ID != "" {
		id = claudeSessionUUID(session.ID)
	}
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
	id := strings.TrimSpace(nativeConversationID)
	if !isUUID(id) {
		return false, nil
	}
	configDir := strings.TrimSpace(env["CLAUDE_CONFIG_DIR"])
	if configDir == "" {
		configDir = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("claude-code: resolve transcript root: %w", err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	projectsDir := filepath.Join(configDir, "projects")
	projects, err := os.ReadDir(projectsDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claude-code: read transcript root %s: %w", projectsDir, err)
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !project.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(projectsDir, project.Name(), id+".jsonl"))
		switch {
		case err == nil && info.Mode().IsRegular() && info.Size() > 0:
			return true, nil
		case err == nil, os.IsNotExist(err):
			continue
		default:
			return false, fmt.Errorf("claude-code: inspect transcript for %s: %w", id, err)
		}
	}
	return false, nil
}

func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func claudeSessionUUID(aoSessionID string) string {
	return agentruntime.ClaudeSessionID(aoSessionID)
}

func SessionUUID(aoSessionID string) string {
	return claudeSessionUUID(aoSessionID)
}

var claudeBinarySpec = binaryutil.BinarySpec{
	Label:         "claude",
	Names:         []string{"claude"},
	WinNames:      []string{"claude.exe", "claude.cmd", "claude"},
	UnixPaths:     []string{"/usr/local/bin/claude", "/opt/homebrew/bin/claude"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("claude", []string{".claude", "local", "claude"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "claude.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "claude.cmd"}},
	},
}

func ResolveClaudeBinary(ctx context.Context) (string, error) {
	binary, err := binaryutil.ResolveBinary(ctx, claudeBinarySpec)
	if err != nil {
		return "", err
	}
	return resolveNativeWindowsClaude(binary, runtime.GOOS), nil
}

func resolveNativeWindowsClaude(path, goos string) string {
	if goos != "windows" || !strings.EqualFold(filepath.Ext(path), ".cmd") {
		return path
	}
	for _, candidate := range windowsNativeClaudeCandidatesForShim(path) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return path
}

func windowsNativeClaudeCandidatesForShim(shim string) []string {
	dir := filepath.Dir(shim)
	candidates := []string{
		filepath.Join(dir, "claude.exe"),
		filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"),
	}
	if current, ok := binaryutil.ManagedCurrentDir(shim, "claude-code"); ok {
		candidates = append(candidates, filepath.Join(current, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"))
	}
	return candidates
}

func (p *Plugin) claudeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveClaudeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func claudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude-code: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

var claudeTrustMu sync.Mutex

func ensureWorkspaceTrusted(configPath, workspacePath string) error {
	return ensureWorkspaceTrustedForOS(configPath, workspacePath, runtime.GOOS)
}

func ensureWorkspaceTrustedForOS(configPath, workspacePath, goos string) error {
	return ensureClaudeLaunchConfigForOS(configPath, workspacePath, false, goos)
}

func ensureClaudeLaunchConfigForOS(configPath, workspacePath string, markAutoDefaultNudgeSeen bool, goos string) error {
	claudeTrustMu.Lock()
	defer claudeTrustMu.Unlock()
	if goos == "windows" {
		workspacePath = strings.ReplaceAll(workspacePath, `\`, "/")
	}

	root := map[string]any{}
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(bytes.TrimSpace(data)) == 0 {
			return fmt.Errorf("claude-code: %s is empty; refusing to overwrite", configPath)
		}
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("claude-code: parse %s: %w", configPath, err)
		}
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("claude-code: read %s: %w", configPath, err)
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

	changed := false
	if trusted, ok := entry["hasTrustDialogAccepted"].(bool); !ok || !trusted {
		entry["hasTrustDialogAccepted"] = true
		changed = true
	}
	if seen, _ := root["hasSeenAutoDefaultNudge"].(bool); markAutoDefaultNudgeSeen && !seen {
		root["hasSeenAutoDefaultNudge"] = true
		changed = true
	}
	if !changed {
		return nil
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("claude-code: encode %s: %w", configPath, err)
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("claude-code: create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return fmt.Errorf("claude-code: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("claude-code: write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("claude-code: close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("claude-code: replace config: %w", err)
	}
	return nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
