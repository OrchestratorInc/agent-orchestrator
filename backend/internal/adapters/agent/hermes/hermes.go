// Package hermes supervises the official Hermes Agent classic interactive CLI.
// It uses native invocation configuration and read-only native leases, never
// installs global plugins or changes native safety/approval implementations.
package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type Plugin struct{ agentbase.Base }

func New() *Plugin { return &Plugin{} }
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "hermes", Name: "Hermes Agent", Description: "Official Nous Research Hermes Agent interactive terminal; native approvals and exact session restore.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("hermes: AO lease observation currently supports Linux only")
	}
	return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "hermes", Names: []string{"hermes"}, UnixHomePaths: [][]string{{".local", "bin", "hermes"}, {".hermes", "hermes-agent", ".venv", "bin", "hermes"}}, ValidateIdentity: func(ctx context.Context, path string) bool {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := aoprocess.CommandContext(ctx, path, "--version")
		cmd.WaitDelay = 100 * time.Millisecond
		b, err := cmd.CombinedOutput()
		return err == nil && strings.Contains(string(b), "Hermes Agent v0.21.6")
	}})
}
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native Hermes --model override; provider and credentials remain native configuration.")
}
func validate(cfg ports.LaunchConfig) error {
	if len(cfg.AllowedTools)+len(cfg.DisallowedTools) > 0 {
		return fmt.Errorf("hermes: AO tool allow/deny restrictions are unsupported")
	}
	switch cfg.Permissions {
	case "", ports.PermissionModeDefault, ports.PermissionModeBypassPermissions:
		return nil
	default:
		return fmt.Errorf("hermes: permission mode %q unsupported; use native default or explicit bypass", cfg.Permissions)
	}
}
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "chat", "--cli", "--in", cfg.WorkspacePath}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if cfg.Permissions == ports.PermissionModeBypassPermissions {
		cmd = append(cmd, "--yolo")
	}
	if cfg.Prompt != "" {
		dir := sessionDir(cfg.DataDir, cfg.SessionID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "initial-query.txt")
		if err := os.WriteFile(path, []byte(cfg.Prompt), 0600); err != nil {
			return nil, err
		}
		cmd = append(cmd, "--query-file", path)
	}
	return cmd, nil
}
func sessionDir(data, id string) string { return filepath.Join(data, "agents", "hermes", id) }

// GetAgentHooks only supplies process-local additive native configuration. The
// name is AO's existing launch-environment boundary; no native hooks are added.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Env == nil {
		return fmt.Errorf("hermes: launch environment required")
	}
	home, err := nativeconfig.Resolve(cfg.Env, "HERMES_HOME", ".hermes")
	if err != nil {
		return err
	}
	if filepath.Base(filepath.Dir(home)) == "profiles" {
		return fmt.Errorf("hermes: named native profiles are not supported by this initial rendered-composer integration")
	}
	// Native sticky profiles can redirect a root even when HERMES_HOME is set.
	if filepath.Base(filepath.Dir(home)) != "profiles" {
		if b, e := os.ReadFile(filepath.Join(home, "active_profile")); e == nil && strings.TrimSpace(string(b)) != "" && strings.TrimSpace(string(b)) != "default" {
			return fmt.Errorf("hermes: sticky native profile requires an explicit HERMES_HOME pointing to profiles/<name>")
		}
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
		original, exists = cfg.Env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]
	}
	if !exists {
		original = os.Getenv("HERMES_EPHEMERAL_SYSTEM_PROMPT")
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
		original, err = nativeOverlay(config)
		if err != nil {
			return err
		}
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
	if len(original)+len(instructions) > 128*1024 {
		return fmt.Errorf("hermes: standing instructions exceed process environment limit")
	}
	if instructions != "" {
		if original != "" {
			original += "\n\n"
		}
		cfg.Env["HERMES_EPHEMERAL_SYSTEM_PROMPT"] = original + instructions
	}
	dir := sessionDir(cfg.DataDir, cfg.SessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "native-home"), []byte(home), 0600)
}
func nativeOverlay(config map[string]any) (string, error) {
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
				return strings.TrimSpace(strings.Join(parts, "\n")), nil
			}
			return promptText(v), nil
		}
	}
	return promptText(agent["system_prompt"]), nil
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
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if id == "" || id == "latest" || strings.ContainsAny(id, " /\\\r\n\t") {
		return nil, false, fmt.Errorf("hermes: exact observed native session ID required")
	}
	home, err := nativeHome(cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, err
	}
	terminal, err := os.ReadFile(filepath.Join(sessionDir(cfg.DataDir, cfg.Session.ID), "observed-terminal"))
	if err != nil {
		return nil, false, fmt.Errorf("hermes: current terminal identity was not observed")
	}
	crumb, err := readBreadcrumb(string(terminal))
	if err != nil || crumb.SessionID != id {
		return nil, false, fmt.Errorf("hermes: native terminal rotated after observation; refusing stale restore")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(home, "state.db"))+"?mode=ro")
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	var cwd string
	var messages int
	var children int
	err = db.QueryRowContext(ctx, "SELECT COALESCE(cwd,''), (SELECT count(*) FROM messages WHERE session_id=sessions.id), (SELECT count(*) FROM sessions child WHERE child.parent_session_id=sessions.id) FROM sessions WHERE id=?", id).Scan(&cwd, &messages, &children)
	if err != nil || messages == 0 || children != 0 || filepath.Clean(cwd) != filepath.Clean(cfg.Session.WorkspacePath) {
		return nil, false, fmt.Errorf("hermes: native history missing, compressed, or workspace mismatch")
	}
	cmd, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{DataDir: cfg.DataDir, SessionID: cfg.Session.ID, WorkspacePath: cfg.Session.WorkspacePath, Config: cfg.Config, Permissions: cfg.Permissions, AllowedTools: cfg.AllowedTools, DisallowedTools: cfg.DisallowedTools})
	if err != nil {
		return nil, false, err
	}
	return append(cmd, "--resume", id), true, nil
}
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	return p.readSessionInfo(ctx, ref, "")
}
func (*Plugin) readSessionInfo(ctx context.Context, ref ports.SessionRef, launchID string) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	home, err := nativeHome(ref.DataDir, ref.ID)
	if err != nil {
		return ports.SessionInfo{}, false, err
	}
	b, err := os.ReadFile(filepath.Join(home, "runtime", "active_sessions.json"))
	if os.IsNotExist(err) {
		return ports.SessionInfo{}, false, nil
	}
	if err != nil {
		return ports.SessionInfo{}, false, err
	}
	var leases struct {
		Entries []struct {
			SessionID string  `json:"session_id"`
			PID       int     `json:"pid"`
			Start     float64 `json:"process_start_time"`
		}
	}
	if err := json.Unmarshal(b, &leases); err != nil {
		return ports.SessionInfo{}, false, err
	}
	found := ""
	for _, lease := range leases.Entries {
		if !leaseProcessMatches(lease.PID, lease.Start) {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(lease.PID), "environ"))
		if err != nil {
			continue
		}
		match := false
		launchMatch := launchID == ""
		for _, entry := range strings.Split(string(env), "\x00") {
			if entry == "AO_SESSION_ID="+ref.ID {
				match = true
			}
			if entry == "AO_RUNTIME_LAUNCH_ID="+launchID {
				launchMatch = true
			}
		}
		if !match || !launchMatch {
			continue
		}
		// The lease proves process ownership only. /new and compression rotate
		// the native terminal breadcrumb without transferring the startup lease.
		tty, e := os.Readlink(filepath.Join("/proc", strconv.Itoa(lease.PID), "fd", "0"))
		if e != nil || !strings.HasPrefix(tty, "/dev/pts/") {
			continue
		}
		key := "tty-" + regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(strings.Trim(tty, "/"), "-")
		crumbPath := filepath.Join(home, "terminal-sessions", key)
		crumb, e := readBreadcrumb(crumbPath)
		if e != nil || crumb.Timestamp < lease.Start {
			continue
		}
		cwd, e := os.Readlink(filepath.Join("/proc", strconv.Itoa(lease.PID), "cwd"))
		if e != nil || filepath.Clean(cwd) != filepath.Clean(crumb.Cwd) {
			continue
		}
		db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(home, "state.db"))+"?mode=ro")
		if e != nil {
			continue
		}
		var storedCwd string
		e = db.QueryRowContext(ctx, "SELECT COALESCE(cwd,'') FROM sessions WHERE id=?", crumb.SessionID).Scan(&storedCwd)
		db.Close()
		if e != nil || filepath.Clean(storedCwd) != filepath.Clean(cwd) {
			continue
		}
		if found != "" && found != crumb.SessionID {
			return ports.SessionInfo{}, false, fmt.Errorf("hermes: ambiguous current native identity")
		}
		if e = os.WriteFile(filepath.Join(sessionDir(ref.DataDir, ref.ID), "observed-terminal"), []byte(crumbPath), 0600); e != nil {
			return ports.SessionInfo{}, false, e
		}
		found = crumb.SessionID
	}
	if found == "" {
		return ports.SessionInfo{}, false, nil
	}
	return ports.SessionInfo{AgentSessionID: found}, true, nil
}
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, key := range []string{"ZAI_API_KEY", "GLM_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"} {
		if os.Getenv(key) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}
func (*Plugin) ContinuouslyDetectTerminalActivity() bool   { return true }
func (*Plugin) EmptyComposerProvesWaitingInputReady() bool { return true }
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	// Native placeholders use italic; typed composer text has normal styling.
	composerOutput := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllStringFunc(output, func(sequence string) string {
		params := strings.Split(strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m"), ";")
		for _, param := range params {
			if param == "3" {
				return strings.TrimSuffix(sequence, "m") + ";2m"
			}
			if param == "23" {
				return strings.TrimSuffix(sequence, "m") + ";22m"
			}
		}
		return sequence
	})
	lines := terminalui.PlainTerminalLines(output)
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-16; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "⚠ ❯") || strings.HasPrefix(line, "? ❯") || strings.Contains(line, "Dangerous Command") || strings.Contains(line, "Allow once") || strings.Contains(line, "Help improve Hermes?") || strings.Contains(line, "Collect locally only") || strings.Contains(line, "Allow this command?") || strings.Contains(line, "[1] Allow once") || strings.Contains(line, "type secret (hidden)") || strings.Contains(line, "type password (hidden)") || strings.Contains(line, "type your answer here") {
			return domain.ActivityBlocked, true
		}
		if strings.Contains(line, "❯") && strings.Contains(line, "msg=interrupt") {
			return domain.ActivityActive, true
		}
		if strings.HasPrefix(line, "❯") || strings.HasPrefix(line, "☤ ❯") {
			if terminalui.LastPromptIsEmptyOrDimPlaceholder(composerOutput, "❯") || terminalui.LastPromptIsEmptyOrDimPlaceholder(composerOutput, "☤ ❯") {
				return domain.ActivityIdle, true
			}
			return "", false
		}
	}
	return "", false
}

func (*Plugin) RequiresRenderedActivity() bool { return true }
func (p *Plugin) ResolveNativeSessionID(ctx context.Context, cfg ports.NativeSessionResolveConfig) (string, bool, error) {
	info, ok, err := p.readSessionInfo(ctx, ports.SessionRef{ID: string(cfg.SessionID), DataDir: cfg.DataDir}, cfg.LaunchID)
	return info.AgentSessionID, ok, err
}

// Linux exports process start ticks relative to the same integer boot time
// psutil uses for the native lease. Missing/changed identities fail closed.
func leaseProcessMatches(pid int, expected float64) bool {
	if pid <= 0 || expected <= 0 {
		return false
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	end := strings.LastIndex(string(stat), ")")
	if end < 0 {
		return false
	}
	fields := strings.Fields(string(stat)[end+1:])
	if len(fields) < 20 || fields[0] == "Z" {
		return false
	}
	ticks, err := strconv.ParseFloat(fields[19], 64)
	if err != nil {
		return false
	}
	boot, err := os.ReadFile("/proc/stat")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(boot), "\n") {
		if strings.HasPrefix(line, "btime ") {
			b, err := strconv.ParseFloat(strings.TrimPrefix(line, "btime "), 64)
			return err == nil && math.Abs(b+ticks/100-expected) < 0.02
		}
	}
	return false
}

type breadcrumb struct {
	SessionID string  `json:"session_id"`
	Cwd       string  `json:"cwd"`
	Timestamp float64 `json:"ts"`
}

func readBreadcrumb(path string) (breadcrumb, error) {
	var b breadcrumb
	raw, e := os.ReadFile(path)
	if e != nil {
		return b, e
	}
	e = json.Unmarshal(raw, &b)
	if b.SessionID == "" {
		return b, fmt.Errorf("hermes: empty native breadcrumb")
	}
	return b, e
}
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}
