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
	"slices"
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

// versionProbeTimeout bounds the identity probe. `jarvis --version` imports the
// full Python dependency tree, which takes 10s+ cold on a loaded host.
const versionProbeTimeout = 30 * time.Second

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
		ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
		defer cancel()
		cmd := aoprocess.CommandContext(ctx, path, "--version")
		cmd.WaitDelay = 100 * time.Millisecond
		b, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			identityErr = fmt.Errorf("native --version probe of %s timed out: %w", path, ctx.Err())
			return false
		}
		if err != nil {
			identityErr = fmt.Errorf("native --version probe: %w", err)
			return false
		}
		if !reportsPinnedVersion(string(b)) {
			identityErr = fmt.Errorf("native CLI did not report pinned OpenJarvis 1.0.4 identity")
			return false
		}
		return true
	}})
	if resolveErr != nil && identityErr != nil {
		return "", fmt.Errorf("%w: %w", resolveErr, identityErr)
	}
	return path, resolveErr
}

// pinnedVersionLine is the exact `jarvis --version` line of the qualified release.
const pinnedVersionLine = "jarvis, version 1.0.4"

// reportsPinnedVersion requires a whole output line to be the pinned version,
// so a later 1.0.40 release or a lookalike `jarvis` that merely mentions it is
// refused. Other lines (Python warnings on stderr) are ignored.
func reportsPinnedVersion(output string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == pinnedVersionLine {
			return true
		}
	}
	return false
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

// resolveLaunchBinary finds the qualified CLI for a launch; tests replace it.
var resolveLaunchBinary = func(ctx context.Context, p *Plugin) (string, error) { return p.ResolveBinary(ctx) }

// GetLaunchCommand implements the scoped native adapter contract.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(cfg.AllowedTools)+len(cfg.DisallowedTools) > 0 {
		return nil, fmt.Errorf("openjarvis: AO tool restriction overrides unsupported; configure native tools/security")
	}
	// OpenJarvis has no per-launch approval flags; every AO mode runs with the
	// native security policy and confirmations.
	if cfg.Prompt != "" {
		if err := validateLine(cfg.Prompt); err != nil {
			return nil, err
		}
	}
	binary, err := resolveLaunchBinary(ctx, p)
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

// PromptReadinessHints waits for the native composer. Its "You> " row is
// unterminated, so it only appears in the rendered current viewport.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{Patterns: []string{"You>"}, Timeout: 60 * time.Second, PollInterval: 100 * time.Millisecond, Lines: 40, RenderedScreen: true}, ctx.Err()
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
	home, err := resolveNativePath(nativeHome(cfg.Env), cfg.WorkspacePath, cfg.Env)
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
	config := map[string]any{}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := toml.Unmarshal(b, &config); err != nil {
			return fmt.Errorf("openjarvis: invalid native configuration: %w", err)
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("openjarvis: read native configuration: %w", err)
	}
	applyCodeAssistantDefaults(config, cfg.Env)
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

// TerminalActivityOptions requests styled terminal evidence and native
// decision-card (blocked) detection, matching main's activity-observer contract
// (this replaces the former RequiresRenderedActivity hook).
func (*Plugin) TerminalActivityOptions() ports.TerminalActivityOptions {
	return ports.TerminalActivityOptions{PreferStyledOutput: true, SupportsBlocked: true}
}

// ContinuouslyDetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// EmptyComposerProvesWaitingInputReady implements the scoped native adapter contract.
func (*Plugin) EmptyComposerProvesWaitingInputReady() bool { return true }

// agentFailureMarker is the line the native REPL prints when a turn fails.
const agentFailureMarker = "Agent 'orchestrator' failed:"

// promptRow reports a native composer row. Only a prompt at column zero is a
// composer: embedded You> in echoed task data must never make a busy input()
// turn look empty.
func promptRow(line string) bool {
	return strings.HasPrefix(line, "You>") && (len(line) == 4 || line[4] == ' ')
}

// lastPromptRow is the index of the last composer row, or -1.
func lastPromptRow(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if promptRow(lines[i]) {
			return i
		}
	}
	return -1
}

// DetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := terminalui.PlainTerminalLines(output)
	prompt := lastPromptRow(lines)
	if prompt < 0 {
		if strings.Contains(strings.Join(lines, "\n"), agentFailureMarker) {
			return domain.ActivityBlocked, true
		}
		return "", false
	}
	// Only the current turn can be failed: a failure reported before a fresh
	// prompt is history, and the submitted line itself is task text.
	if strings.Contains(strings.Join(lines[prompt+1:], "\n"), agentFailureMarker) {
		return domain.ActivityBlocked, true
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

// TerminalMessageObserved requires the submitted line in the composer that was
// empty in the admission baseline. That composer sits right below the
// baseline's history, scrolled up by any rows printed since, so a historical
// echo (including an identical prior turn) is never sufficient. Rows the turn
// prints after the echo, and its next empty prompt, are ignored.
func (*Plugin) TerminalMessageObserved(message, baseline, screen string) bool {
	if screen == baseline {
		return false
	}
	before := terminalui.PlainTerminalLines(baseline)
	composer := lastPromptRow(before)
	if composer < 0 {
		return false
	}
	history := before[:composer]
	lines := terminalui.PlainTerminalLines(screen)
	// Repeated content can align at several scroll offsets. The least-scrolled
	// one is taken, so ambiguity can only leave a submission unconfirmed.
	for i := min(len(history), len(lines)-1); i >= 0; i-- {
		if slices.Equal(lines[:i], history[len(history)-i:]) {
			row := lines[i]
			return promptRow(row) && strings.TrimSpace(row) != "You>" && echoStartsAt(lines[i:], "You> "+message)
		}
	}
	return false
}

// echoStartsAt matches physical wrapped rows against the exact submitted byte
// sequence. A row boundary may consume native padding, but never changes user
// data. Blank rows, and every row after the echo, are ignored.
func echoStartsAt(rows []string, expected string) bool {
	for _, row := range rows {
		if expected == "" {
			return true
		}
		if strings.TrimSpace(row) == "" {
			continue
		}
		if !strings.HasPrefix(expected, row) {
			// The row completing the echo may carry trailing cell padding.
			return strings.TrimRight(row, " ") == expected
		}
		expected = expected[len(row):]
	}
	return expected == ""
}

// nativeHome is the unresolved OpenJarvis home for env.
func nativeHome(env map[string]string) string {
	if home := envValue(env, "OPENJARVIS_HOME"); home != "" {
		return home
	}
	if x := envValue(env, "XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "openjarvis")
	}
	return filepath.Join(envValue(env, "HOME"), ".openjarvis")
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

// codeAssistantTools are the native tools AO sessions need; they match the
// documented code-assistant preset, which the PyPI package does not ship.
var codeAssistantTools = []string{"file_read", "file_write", "shell_exec", "code_interpreter"}

// cloudProviderKeys are the variables the native cloud engine reads, in its
// own client order. The model is a default for that provider when the user has
// not chosen one; OpenJarvis's built-in fallback model is retired for Gemini.
var cloudProviderKeys = []struct{ env, model string }{
	{"OPENAI_API_KEY", ""},
	{"ANTHROPIC_API_KEY", ""},
	{"GEMINI_API_KEY", "gemini-3-flash-preview"},
	{"GOOGLE_API_KEY", "gemini-3-flash-preview"},
	{"OPENROUTER_API_KEY", ""},
	{"MINIMAX_API_KEY", ""},
	{"DEEPSEEK_API_KEY", ""},
}

// cloudProviderKey returns the first provider key set for the launch.
func cloudProviderKey(env map[string]string) string {
	for _, provider := range cloudProviderKeys {
		if strings.TrimSpace(envValue(env, provider.env)) != "" {
			return provider.env
		}
	}
	return ""
}

// applyCodeAssistantDefaults fills only what the user has not configured: the
// code-assistant tools, and the cloud engine plus a working model when a
// provider key is available and no engine is chosen.
func applyCodeAssistantDefaults(config map[string]any, env map[string]string) {
	tools, _ := config["tools"].(map[string]any)
	if tools == nil {
		tools = map[string]any{}
		config["tools"] = tools
	}
	enabled := nativeTools(tools["enabled"])
	if len(enabled) == 0 {
		agent, _ := config["agent"].(map[string]any)
		enabled = nativeTools(agent["tools"])
	}
	for _, tool := range codeAssistantTools {
		if !slices.Contains(enabled, tool) {
			enabled = append(enabled, tool)
		}
	}
	tools["enabled"] = enabled

	key := cloudProviderKey(env)
	if key == "" {
		return
	}
	engine, _ := config["engine"].(map[string]any)
	if engine == nil {
		engine = map[string]any{}
		config["engine"] = engine
	}
	if engine["default"] == nil || engine["default"] == "" {
		engine["default"] = "cloud"
	}
	if engine["default"] != "cloud" {
		return
	}
	intelligence, _ := config["intelligence"].(map[string]any)
	if intelligence == nil {
		intelligence = map[string]any{}
		config["intelligence"] = intelligence
	}
	if model, _ := intelligence["default_model"].(string); model != "" {
		return
	}
	for _, provider := range cloudProviderKeys {
		if provider.env == key && provider.model != "" {
			intelligence["default_model"] = provider.model
		}
	}
}
