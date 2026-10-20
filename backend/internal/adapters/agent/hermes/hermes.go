// Package hermes supervises the official Hermes Agent classic interactive CLI.
// It uses native invocation configuration and AO-titled native sessions, never
// installs global plugins or changes native safety/approval implementations.
package hermes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // Register the driver for read-only native history queries.
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Plugin supervises the official native interactive terminal.
type Plugin struct{ agentbase.Base }

// Revalidate these chrome markers whenever the supported native version range changes.
const (
	nativePrompt         = "❯"
	nativeNamedPrompt    = "☤ ❯"
	nativeWarningPrompt  = "⚠ ❯"
	nativeQuestionPrompt = "? ❯"
	nativeUserEcho       = "● "
	nativeCommandEcho    = "⚙️  "
	nativeTranscriptRule = "────────────────────────────────────────"
)

var (
	// v0.21.6 through v0.22.x share the classic CLI chrome and session flags AO
	// drives. Source installs append a date, channel, or commit after a space.
	nativeVersionLine   = regexp.MustCompile(`^Hermes Agent v0\.(?:21\.(?:[6-9]|[1-9][0-9]+)|22\.[0-9]+)(?:\+[0-9A-Za-z.]+)?(?: .*)?$`)
	titleUnsafe         = regexp.MustCompile(`[^A-Za-z0-9._-]`)
	nativeStyleSequence = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
)

// New creates the native TUI adapter.
func New() *Plugin { return &Plugin{} }

// Manifest implements the scoped native adapter contract.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "hermes", Name: "Hermes Agent", Description: "Official Nous Research Hermes Agent interactive terminal; native approvals and exact session restore.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// ResolveBinary implements the scoped native adapter contract.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	timedOut := false
	// Probe only the official installers' locations. A PATH-first --version probe
	// would execute an unrelated earlier binary during passive agent discovery.
	path, err := binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "hermes", UnixPaths: []string{"/usr/local/bin/hermes", "/usr/bin/hermes"}, UnixHomePaths: [][]string{
		{".local", "bin", "hermes"},
		{".hermes", "hermes-agent", ".hermes", "bin", "hermes"},
		{".hermes", "hermes-agent", ".venv", "bin", "hermes"},
	}, WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinLocalAppData, Parts: []string{"hermes", "bin", "hermes.exe"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"hermes", "bin", "hermes.cmd"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"hermes", "hermes-agent", "venv", "Scripts", "hermes.exe"}},
	}, ValidateIdentity: func(ctx context.Context, path string) bool {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := aoprocess.CommandContext(probeCtx, path, "--version")
		aoprocess.ConfigureTreeCancellation(cmd)
		cmd.WaitDelay = 100 * time.Millisecond
		b, err := cmd.CombinedOutput()
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			timedOut = true
			return false
		}
		if probeCtx.Err() != nil {
			return false
		}
		firstLine, _, _ := strings.Cut(string(b), "\n")
		return err == nil && nativeVersionLine.MatchString(strings.TrimSuffix(firstLine, "\r"))
	}})
	if err != nil && timedOut {
		return "", fmt.Errorf("hermes: native identity check timed out: %w", err)
	}
	return path, err
}

// GetConfigSpec implements the scoped native adapter contract.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelEffortConfigSpec(ctx,
		"Native Hermes --model override; provider and credentials remain native configuration.",
		"Native Hermes --reasoning level: none, minimal, low, medium, high, xhigh, max, or ultra.")
}
func validate(cfg ports.LaunchConfig) error {
	if len(cfg.AllowedTools)+len(cfg.DisallowedTools) > 0 {
		return fmt.Errorf("hermes: AO tool allow/deny restrictions are unsupported")
	}
	return nil
}

// permissionArgs maps AO approval modes onto native Hermes approvals. Hermes has
// no accept-edits or auto tier, so those keep native prompts rather than
// approving more than requested; only explicit bypass skips them.
func permissionArgs(mode ports.PermissionMode) []string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeBypassPermissions {
		return []string{"--yolo"}
	}
	return nil
}

// GetLaunchCommand implements the scoped native adapter contract.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	// Every fresh launch mints a new title, so a relaunch never adopts an older
	// conversation; restore reuses the stored one.
	title, err := newSessionTitle(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	cmd, err := p.sessionCommand(ctx, cfg, title, true)
	if err != nil {
		return nil, err
	}
	if cfg.Prompt != "" {
		dir := sessionDir(cfg.DataDir, cfg.SessionID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "initial-query.txt")
		if err := os.WriteFile(path, []byte(cfg.Prompt), 0o600); err != nil {
			return nil, err
		}
		cmd = append(cmd, "--query-file", path)
	}
	return cmd, nil
}
func sessionDir(data, id string) string { return filepath.Join(data, "agents", "hermes", id) }

// sessionCommand starts or resumes the native conversation AO titled for this
// session. Hermes records the title as user-provided, so auto-titling never
// renames it, and resolves it forward through context compression itself.
func (p *Plugin) sessionCommand(ctx context.Context, cfg ports.LaunchConfig, title string, create bool) ([]string, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "chat", "--cli", "--in", cfg.WorkspacePath, "--continue", title}
	if create {
		cmd = append(cmd, "--create-if-missing")
	}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	agentbase.AppendEffortFlag(&cmd, cfg.Config, "--reasoning")
	return append(cmd, permissionArgs(cfg.Permissions)...), nil
}

// newSessionTitle records a fresh native title for an AO launch. The random
// suffix keeps titles unique when several AO data directories share one
// Hermes home and reuse session IDs.
func newSessionTitle(data, id string) (string, error) {
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	safeID := titleUnsafe.ReplaceAllString(id, "-")
	if len(safeID) > 64 {
		safeID = safeID[:64]
	}
	title := "ao-" + safeID + "-" + hex.EncodeToString(suffix)
	dir := sessionDir(data, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "native-title"), []byte(title), 0o600); err != nil {
		return "", err
	}
	return title, nil
}

func sessionTitle(data, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(sessionDir(data, id), "native-title"))
	if err != nil {
		return "", err
	}
	if title := strings.TrimSpace(string(b)); title != "" {
		return title, nil
	}
	return "", fmt.Errorf("hermes: empty native session title")
}

// resolveHome mirrors Hermes's own HERMES_HOME default: ~/.hermes on macOS and
// Linux, %LOCALAPPDATA%\hermes on Windows. The launch env wins over the daemon's.
func resolveHome(env map[string]string) (string, error) {
	if runtime.GOOS != "windows" {
		return nativeconfig.Resolve(env, "HERMES_HOME", ".hermes")
	}
	value, ok := env["HERMES_HOME"]
	if !ok {
		value = os.Getenv("HERMES_HOME")
	}
	if dir := strings.TrimSpace(value); dir != "" {
		return filepath.Clean(dir), nil
	}
	local := strings.TrimSpace(env["LOCALAPPDATA"])
	if local == "" {
		local = os.Getenv("LOCALAPPDATA")
	}
	if local == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		local = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(local, "hermes"), nil
}

// GetAgentHooks only supplies process-local additive native configuration. The
// name is AO's existing launch-environment boundary; no native hooks are added.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Env == nil {
		return fmt.Errorf("hermes: launch environment required")
	}
	home, err := resolveHome(cfg.Env)
	if err != nil {
		return err
	}
	if filepath.Base(filepath.Dir(home)) == "profiles" {
		return fmt.Errorf("hermes: named native profiles are not supported by this initial rendered-composer integration")
	}
	// Native sticky profiles can redirect a root even when HERMES_HOME is set.
	if b, e := os.ReadFile(filepath.Join(home, "active_profile")); e == nil && strings.TrimSpace(string(b)) != "" && strings.TrimSpace(string(b)) != "default" {
		return fmt.Errorf("hermes: sticky native profiles are not supported; clear active_profile or select the default native profile")
	}
	for _, managed := range []string{os.Getenv("HERMES_MANAGED_DIR"), cfg.Env["HERMES_MANAGED_DIR"], "/etc/hermes"} {
		if managed != "" {
			if st, e := os.Stat(managed); e == nil && st.IsDir() {
				return fmt.Errorf("hermes: managed native configuration is not supported by the additive instruction adapter")
			}
		}
	}
	cfg.Env["HERMES_HOME"] = home
	original, exists := cfg.Env["AO_HERMES_ORIGINAL_EPHEMERAL"]
	if !exists {
		original = cfg.Env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]
	}
	if original == "" {
		b, readErr := os.ReadFile(filepath.Join(home, "config.yaml"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if strings.Contains(string(b), "${") {
			return fmt.Errorf("hermes: environment-expanded native instruction configuration is unsupported; preserve it in an explicit native ephemeral prompt")
		}
		var config map[string]any
		if len(b) > 0 {
			if err := yaml.Unmarshal(b, &config); err != nil {
				return fmt.Errorf("hermes: invalid native configuration: %w", err)
			}
		}
		original = nativeOverlay(config)
	}
	cfg.Env["AO_HERMES_ORIGINAL_EPHEMERAL"] = original
	instructions := cfg.SystemPrompt
	if instructions == "" && cfg.SystemPromptFile != "" {
		b, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return err
		}
		instructions = string(b)
	}
	combined := original + instructions
	if original != "" && instructions != "" {
		combined = original + "\n\n" + instructions
	}
	// Linux MAX_ARG_STRLEN counts NAME= and the trailing NUL for each entry;
	// Windows caps a variable at 32,767 characters. The AO original-prompt
	// entry carries a second copy of the native text.
	maxEnvString := 128 * 1024
	if runtime.GOOS == "windows" {
		maxEnvString = 32767
	}
	if len("HERMES_EPHEMERAL_SYSTEM_PROMPT=")+len(combined)+1 > maxEnvString ||
		len("AO_HERMES_ORIGINAL_EPHEMERAL=")+len(original)+1 > maxEnvString {
		return fmt.Errorf("hermes: standing instructions exceed process environment limit")
	}
	if instructions != "" {
		if original != "" {
			original += "\n\n"
		}
		cfg.Env["HERMES_EPHEMERAL_SYSTEM_PROMPT"] = original + instructions
	}
	dir := sessionDir(cfg.DataDir, cfg.SessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "native-home"), []byte(home), 0o600)
}
func nativeOverlay(config map[string]any) string {
	agent, _ := config["agent"].(map[string]any)
	display, _ := config["display"].(map[string]any)
	name := strings.ToLower(strings.TrimSpace(fmt.Sprint(display["personality"])))
	if name != "" && name != "<nil>" && name != "none" && name != "default" && name != "neutral" {
		definitions := map[string]any{}
		for k, v := range builtinPersonalities {
			definitions[k] = v
		}
		for _, src := range []any{config["personalities"], agent["personalities"]} {
			if m, ok := src.(map[string]any); ok {
				for k, v := range m {
					definitions[strings.ToLower(strings.TrimSpace(k))] = v
				}
			}
		}
		if v, ok := definitions[name]; ok {
			if m, ok := v.(map[string]any); ok {
				parts := []string{promptText(m["system_prompt"])}
				for _, k := range []string{"tone", "style"} {
					if s := promptText(m[k]); s != "" {
						parts = append(parts, strings.ToUpper(k[:1])+k[1:]+": "+s)
					}
				}
				return strings.TrimSpace(strings.Join(parts, "\n"))
			}
			return promptText(v)
		}
	}
	return promptText(agent["system_prompt"])
}
func promptText(v any) string {
	if v == nil {
		return ""
	}
	if a, ok := v.([]any); ok {
		var parts []string
		for _, item := range a {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func nativeHome(data, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(sessionDir(data, id), "native-home"))
	return string(b), err
}

// GetRestoreCommand implements the scoped native adapter contract.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	launch := ports.LaunchConfig{DataDir: cfg.DataDir, SessionID: cfg.Session.ID, WorkspacePath: cfg.Session.WorkspacePath, Config: cfg.Config, Permissions: cfg.Permissions, AllowedTools: cfg.AllowedTools, DisallowedTools: cfg.DisallowedTools}
	if err := validate(launch); err != nil {
		return nil, false, err
	}
	title, err := sessionTitle(cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, fmt.Errorf("hermes: AO native session title unavailable: %w", err)
	}
	if _, err := p.titledSessionID(ctx, cfg.DataDir, cfg.Session.ID, title); err != nil {
		return nil, false, err
	}
	// Without --create-if-missing, a conversation renamed or deleted after this
	// check fails loudly instead of silently starting a fresh one.
	cmd, err := p.sessionCommand(ctx, launch, title, false)
	if err != nil {
		return nil, false, err
	}
	return cmd, true, nil
}

// titledSessionID reads the native conversation AO titled for a session.
// Compression continuations keep the root's lineage, which Hermes resolves on
// resume; the root ID is the stable identity AO reports.
func (*Plugin) titledSessionID(ctx context.Context, data, id, title string) (string, error) {
	home, err := nativeHome(data, id)
	if err != nil {
		return "", fmt.Errorf("hermes: native home was not recorded: %w", err)
	}
	db, err := sql.Open("sqlite", nativeStateDSN(home))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	var nativeID string
	err = db.QueryRowContext(ctx, "SELECT id FROM sessions WHERE title=? ORDER BY started_at LIMIT 1", title).Scan(&nativeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("hermes: native conversation %q not found; it may have been renamed or deleted in Hermes", title)
	}
	if err != nil {
		var coded interface{ Code() int }
		if errors.As(err, &coded) && coded.Code() == sqlite3.SQLITE_READONLY_RECOVERY {
			return "", fmt.Errorf("hermes: native state database needs WAL recovery; resume Hermes natively once, then retry restore: %w", err)
		}
		return "", fmt.Errorf("hermes: native state database unavailable: %w", err)
	}
	return nativeID, nil
}

// SessionInfo implements the scoped native adapter contract.
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	id, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: ref.DataDir, SessionID: domain.SessionID(ref.ID)})
	return ports.SessionInfo{AgentSessionID: id}, ok, err
}

func nativeStateDSN(home string) string {
	path := filepath.ToSlash(filepath.Join(home, "state.db"))
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(1000)"
}

// AuthStatus implements the scoped native adapter contract. Hermes keeps API
// keys in <HERMES_HOME>/.env and OAuth credentials in auth.json; their presence
// is configured, not verified authorization.
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, key := range []string{"OPENROUTER_API_KEY", "NOUS_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "ZAI_API_KEY", "GLM_API_KEY", "KIMI_API_KEY", "MINIMAX_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY", "HF_TOKEN"} {
		if os.Getenv(key) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	home, err := resolveHome(nil)
	if err != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	if envFileHasCredential(filepath.Join(home, ".env")) || authFileHasCredential(filepath.Join(home, "auth.json")) {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func envFileHasCredential(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "export "), "=")
		key = strings.TrimSpace(key)
		if !ok || strings.HasPrefix(key, "#") || strings.Trim(strings.TrimSpace(value), `"'`) == "" {
			continue
		}
		if strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN") {
			return true
		}
	}
	return false
}

func authFileHasCredential(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var store map[string]any
	if json.Unmarshal(b, &store) != nil {
		return false
	}
	for _, value := range store {
		switch v := value.(type) {
		case map[string]any:
			if len(v) > 0 {
				return true
			}
		case []any:
			if len(v) > 0 {
				return true
			}
		}
	}
	return false
}

// ContinuouslyDetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// EmptyComposerProvesWaitingInputReady implements the scoped native adapter contract.
func (*Plugin) EmptyComposerProvesWaitingInputReady() bool { return true }

// DetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	// Native placeholders use italic; typed composer text has normal styling.
	composerOutput := nativeStyleSequence.ReplaceAllStringFunc(output, func(sequence string) string {
		params := strings.Split(strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m"), ";")
		for i := 0; i < len(params); i++ {
			code, _, colon := strings.Cut(params[i], ":")
			// Color payloads contain numbers that are not SGR attributes.
			if code == "38" || code == "48" || code == "58" {
				if !colon && i+1 < len(params) {
					switch params[i+1] {
					case "5":
						i = min(i+2, len(params)-1)
					case "2":
						i = min(i+4, len(params)-1)
					}
				}
				continue
			}
			// Insert beside the real attribute so later resets retain their order.
			switch code {
			case "3":
				params[i] += ";2"
			case "23":
				params[i] += ";22"
			}
		}
		return "\x1b[" + strings.Join(params, ";") + "m"
	})
	lines := terminalui.PlainTerminalLines(output)
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	// The running composer also has borders and an italic placeholder. Inspect
	// the newest prompt first so that chrome cannot satisfy the idle check.
	for i := last; i >= 0 && i >= last-7; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, nativeWarningPrompt) || strings.HasPrefix(line, nativeQuestionPrompt) {
			return domain.ActivityBlocked, true
		}
		if (strings.HasPrefix(line, nativePrompt) || strings.HasPrefix(line, nativeNamedPrompt)) && strings.Contains(line, "msg=interrupt") {
			return domain.ActivityActive, true
		}
		if strings.HasPrefix(line, nativePrompt) || strings.HasPrefix(line, nativeNamedPrompt) {
			break
		}
	}
	if last >= 0 {
		bottom := strings.TrimSpace(lines[last])
		if len([]rune(bottom)) >= 16 && strings.Trim(bottom, "─") == "" &&
			(terminalui.LastBorderedPromptIsEmptyOrDimPlaceholder(composerOutput, nativePrompt) ||
				terminalui.LastBorderedPromptIsEmptyOrDimPlaceholder(composerOutput, nativeNamedPrompt)) {
			return domain.ActivityIdle, true
		}
	}
	for i := last; i >= 0 && i >= last-4; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, nativePrompt) || strings.HasPrefix(line, nativeNamedPrompt) {
			return domain.ActivityBlocked, true
		}
	}
	return "", false
}

// RequiresRenderedActivity implements the scoped native adapter contract.
func (*Plugin) RequiresRenderedActivity() bool { return true }

// ResolveNativeSessionID implements the scoped native adapter contract.
func (p *Plugin) ResolveNativeSessionID(ctx context.Context, cfg ports.NativeSessionResolveConfig) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	title, err := sessionTitle(cfg.DataDir, string(cfg.SessionID))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	id, err := p.titledSessionID(ctx, cfg.DataDir, string(cfg.SessionID), title)
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// ExitDetectionMode implements the scoped native adapter contract.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// ValidateTerminalMessage refuses ordinary AO text while native decisions own input.
func (p *Plugin) ValidateTerminalMessage(_, screen string) error {
	state, ok := p.DetectTerminalActivity(screen)
	if !ok || state != domain.ActivityIdle {
		return fmt.Errorf("hermes: ordinary AO sends require the current empty native composer; enter approval decisions directly in the terminal")
	}
	return nil
}

// TerminalMessageObserved accepts a running footer or a newly committed native
// transcript echo above an empty composer. A changed idle frame alone is not
// evidence of submission: it may be an unrelated redraw before Enter is handled.
func (p *Plugin) TerminalMessageObserved(message, baseline, screen string) bool {
	before, ready := p.DetectTerminalActivity(baseline)
	after, known := p.DetectTerminalActivity(screen)
	if screen == baseline || !ready || before != domain.ActivityIdle || !known {
		return false
	}
	if after == domain.ActivityActive {
		return true
	}
	if after != domain.ActivityIdle {
		return false
	}
	return committedMessageEchoes(message, screen) > committedMessageEchoes(message, baseline)
}

// Count complete matching native echoes outside the current composer. Comparing
// counts prevents old identical text from acknowledging a new send after a redraw.
// Ignore whitespace inserted by terminal line wrapping, but require the entire
// message between native transcript rules. Truncated previews remain unconfirmed.
func committedMessageEchoes(message, screen string) int {
	message = strings.TrimSpace(message)
	if message == "" {
		return 0
	}
	normalizedMessage := strings.Join(strings.Fields(message), "")
	lines := terminalui.PlainTerminalLines(screen)
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, nativePrompt) && !strings.HasPrefix(line, nativeNamedPrompt) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if row := strings.TrimSpace(lines[j]); len([]rune(row)) >= 16 && strings.Trim(row, "─") == "" {
				end = j
				break
			}
		}
		break
	}
	count := 0
	for i := 0; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(message, "/") && !strings.Contains(message, "\n") && line == nativeCommandEcho+message {
			count++
			continue
		}
		if i == 0 || strings.TrimSpace(lines[i-1]) != nativeTranscriptRule || !strings.HasPrefix(line, nativeUserEcho) {
			continue
		}
		var echo strings.Builder
		echo.WriteString(strings.TrimPrefix(line, nativeUserEcho))
		j := i + 1
		for j < end && strings.TrimSpace(lines[j]) != nativeTranscriptRule {
			echo.WriteString(strings.TrimSpace(lines[j]))
			j++
		}
		if j < end && strings.Join(strings.Fields(echo.String()), "") == normalizedMessage {
			count++
		}
	}
	return count
}
