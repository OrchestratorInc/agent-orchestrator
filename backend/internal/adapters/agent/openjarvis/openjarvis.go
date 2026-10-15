// Package openjarvis runs the official OpenJarvis code-assistant chat REPL.
// Native conversation history is process-local: restore, Chat and handoff are
// deliberately unsupported. Native security and interactive confirmations remain
// in control. AO supports one plain input line at an observed empty composer.
package openjarvis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Plugin supervises the official native interactive terminal.
type Plugin struct{ agentbase.Base }

// New creates the native TUI adapter.
func New() *Plugin { return &Plugin{} }

// Manifest implements the scoped native adapter contract.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "openjarvis", Name: "OpenJarvis", Description: "Official OpenJarvis code-assistant terminal. Plain-line input and native confirmations; no persistent conversation restore.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// ResolveBinary implements the scoped native adapter contract.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	var identityErr error
	path, resolveErr := binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "openjarvis", Names: []string{"jarvis"}, UnixHomePaths: [][]string{{".local", "bin", "jarvis"}}, ValidateIdentity: func(ctx context.Context, path string) bool {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := aoprocess.CommandContext(ctx, path, "--version")
		cmd.WaitDelay = 100 * time.Millisecond
		b, err := cmd.CombinedOutput()
		if err != nil {
			identityErr = fmt.Errorf("native --version probe: %w", err)
			return false
		}
		if !strings.Contains(string(b), "jarvis, version 1.0.4") {
			identityErr = fmt.Errorf("native CLI did not report pinned OpenJarvis1.0.4 identity")
			return false
		}
		return true
	}})
	if resolveErr != nil && identityErr != nil {
		return "", fmt.Errorf("%w: %w", resolveErr, identityErr)
	}
	return path, resolveErr
}

// GetConfigSpec implements the scoped native adapter contract.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native --model override. Configure a compatible engine/provider using the OpenJarvis code-assistant preset first.")
}
func validateLine(message string) error {
	if !utf8.ValidString(message) || len(message) > 3000 {
		return fmt.Errorf("%w: openjarvis input must be valid UTF-8 and at most 3000 bytes", ports.ErrAgentInputRejected)
	}
	if strings.TrimSpace(message) != message || message == "" {
		return fmt.Errorf("%w: openjarvis requires a nonempty plain line without surrounding whitespace", ports.ErrAgentInputRejected)
	}
	for _, r := range message {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: openjarvis multiline and control-character input are unsupported", ports.ErrAgentInputRejected)
		}
	}
	if strings.Contains(message, "You>") || strings.Contains(message, "Confirm:") {
		return fmt.Errorf("%w: openjarvis native composer/decision markers are reserved in programmatic input", ports.ErrAgentInputRejected)
	}
	if strings.HasPrefix(message, "/") {
		return fmt.Errorf("%w: openjarvis native slash commands must be entered directly in the terminal", ports.ErrAgentInputRejected)
	}
	return nil
}

// GetLaunchCommand implements the scoped native adapter contract.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(cfg.AllowedTools)+len(cfg.DisallowedTools) > 0 {
		return nil, fmt.Errorf("openjarvis: AO tool restriction overrides unsupported; configure native tools/security")
	}
	if cfg.Permissions != "" && cfg.Permissions != ports.PermissionModeDefault {
		return nil, fmt.Errorf("%w: openjarvis supports only native default permissions", ports.ErrAgentPermissionModeUnsupported)
	}
	if cfg.Prompt != "" {
		if err := validateLine(cfg.Prompt); err != nil {
			return nil, err
		}
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "chat", "--agent", "orchestrator"}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	return cmd, nil
}

// GetPromptDeliveryStrategy implements the scoped native adapter contract.
func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, ctx.Err()
}

// PromptReadinessHints implements the scoped native adapter contract.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{Patterns: []string{"You>"}, Timeout: 60 * time.Second, PollInterval: 100 * time.Millisecond, Lines: 40}, ctx.Err()
}

// GetRestoreCommand implements the scoped native adapter contract.
func (*Plugin) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, fmt.Errorf("%w: openjarvis native chat history is in memory; start a new session explicitly", ports.ErrAgentRestoreUnsupported)
}

// GetAgentHooks implements the scoped native adapter contract.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Env == nil {
		return fmt.Errorf("openjarvis: process environment required")
	}
	home := envValue(cfg.Env, "OPENJARVIS_HOME")
	if home == "" {
		if x := envValue(cfg.Env, "XDG_DATA_HOME"); x != "" {
			home = filepath.Join(x, "openjarvis")
		} else {
			home = filepath.Join(envValue(cfg.Env, "HOME"), ".openjarvis")
		}
	}
	home, err := resolveNativePath(home, cfg.WorkspacePath, cfg.Env)
	if err != nil {
		return err
	}
	path, exists := cfg.Env["AO_OPENJARVIS_SOURCE_CONFIG"]
	if !exists {
		path, exists = cfg.Env["OPENJARVIS_CONFIG"]
	}
	if !exists {
		path = os.Getenv("OPENJARVIS_CONFIG")
	}
	if path == "" {
		path = filepath.Join(home, "config.toml")
	}
	path, err = resolveNativePath(path, cfg.WorkspacePath, cfg.Env)
	if err != nil {
		return err
	}
	cfg.Env["OPENJARVIS_HOME"] = home
	cfg.Env["AO_OPENJARVIS_SOURCE_CONFIG"] = path
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("openjarvis: configure native code-assistant preset first: %w", err)
	}
	var config map[string]any
	if err := toml.Unmarshal(b, &config); err != nil {
		return fmt.Errorf("openjarvis: invalid native configuration: %w", err)
	}
	tools, _ := config["tools"].(map[string]any)
	enabled := nativeTools(tools["enabled"])
	if len(enabled) == 0 {
		agent, _ := config["agent"].(map[string]any)
		enabled = nativeTools(agent["tools"])
	}
	required := map[string]bool{"file_read": false, "file_write": false, "shell_exec": false, "code_interpreter": false}
	for _, tool := range enabled {
		if _, ok := required[tool]; ok {
			required[tool] = true
		}
	}
	for tool, ok := range required {
		if !ok {
			return fmt.Errorf("openjarvis: native code-assistant configuration must enable %s", tool)
		}
	}
	instructions := cfg.SystemPrompt
	if instructions == "" && cfg.SystemPromptFile != "" {
		b, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return err
		}
		instructions = string(b)
	}
	if cfg.WorkspacePath != "" {
		instructions += fmt.Sprintf("\n\nYour actual AO session workspace is %q. Use this workspace for coding and tool paths, rather than the original repository checkout. Verify current process cwd when reporting it.", cfg.WorkspacePath)
	}
	sp, _ := config["system_prompt"].(map[string]any)
	if sp == nil {
		sp = map[string]any{}
		config["system_prompt"] = sp
	}
	prefix, _ := sp["prefix"].(string)
	if instructions != "" {
		if prefix != "" {
			prefix += "\n\n"
		}
		sp["prefix"] = prefix + instructions + "\n\nWhen calling AO CLI commands through shell_exec, preserve the session routing environment using env_passthrough=[\"AO_SESSION_ID\",\"AO_RUN_FILE\",\"AO_PROJECT_ID\",\"AO_DATA_DIR\",\"AO_RUNTIME_LAUNCH_ID\"]. Run in the actual session workspace. This preserves AO routing without passing provider credentials."
	}
	overlay, err := toml.Marshal(config)
	if err != nil {
		return err
	}
	dir := filepath.Join(cfg.DataDir, "agents", "openjarvis", cfg.SessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path = filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, overlay, 0o600); err != nil {
		return err
	}
	cfg.Env["OPENJARVIS_CONFIG"] = path
	return nil
}

// ValidateTerminalMessage is called under the daemon's serialized programmatic
// input admission, against a fresh rendered viewport immediately before writing.
func (p *Plugin) ValidateTerminalMessage(message, screen string) error {
	if err := validateLine(message); err != nil {
		return err
	}
	state, ok := p.DetectTerminalActivity(screen)
	if !ok || state != domain.ActivityIdle {
		return fmt.Errorf("%w: openjarvis terminal is not at an observed empty input composer; active turns and native decisions refuse AO sends", ports.ErrAgentInputRejected)
	}
	return nil
}

// RequiresRenderedActivity implements the scoped native adapter contract.
func (*Plugin) RequiresRenderedActivity() bool { return true }

// ContinuouslyDetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// EmptyComposerProvesWaitingInputReady implements the scoped native adapter contract.
func (*Plugin) EmptyComposerProvesWaitingInputReady() bool { return true }

// DetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := terminalui.PlainTerminalLines(output)
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "Agent 'orchestrator' failed:") {
		return domain.ActivityBlocked, true
	}
	// Only a native prompt at column zero is a composer. Embedded You> in
	// echoed task data must never make a busy input() turn look empty.
	prompt := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "You>") && (len(line) == 4 || line[4] == ' ') {
			prompt = i
		}
	}
	if prompt < 0 {
		return "", false
	}
	current := strings.TrimRight(strings.Join(lines[prompt:], "\n"), " \t\r\n")
	if strings.Contains(current, "Confirm:") {
		return domain.ActivityBlocked, true
	}
	if current == "You>" {
		return domain.ActivityIdle, true
	}
	return domain.ActivityActive, true
}

// AuthStatus implements the scoped native adapter contract.
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

// TerminalMessageObserved requires an actual change from the admission baseline.
// Historical echoes (including identical prior turns) are never sufficient.
func (*Plugin) TerminalMessageObserved(message, baseline, screen string) bool {
	if screen == baseline {
		return false
	}
	lines := terminalui.PlainTerminalLines(screen)
	prompt := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "You> ") {
			prompt = i
		}
	}
	if prompt < 0 {
		return false
	}
	// Match physical wrapped rows against the exact submitted byte sequence.
	// A row boundary may consume native padding, but never changes user data.
	expected := "You> " + message
	for _, line := range lines[prompt:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > len(expected) || !strings.HasPrefix(expected, line) {
			return false
		}
		expected = expected[len(line):]
	}
	return expected == ""
}
func envValue(env map[string]string, key string) string {
	if v, ok := env[key]; ok {
		return v
	}
	return os.Getenv(key)
}
func resolveNativePath(path, workspace string, env map[string]string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home := envValue(env, "HOME")
		if home == "" {
			return "", fmt.Errorf("openjarvis: native HOME unavailable")
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	return filepath.Abs(path)
}

// ExitDetectionMode implements the scoped native adapter contract.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

func nativeTools(value any) []string {
	var names []string
	switch v := value.(type) {
	case string:
		names = strings.Split(v, ",")
	case []any:
		for _, item := range v {
			names = append(names, fmt.Sprint(item))
		}
	}
	var clean []string
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			clean = append(clean, name)
		}
	}
	return clean
}
