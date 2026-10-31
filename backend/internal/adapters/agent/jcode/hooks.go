package jcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	overlayFileName = "prompt-overlay.md"
	overlaySentinel = "<!-- agent-orchestrator: managed jcode instructions -->"
	hookCommandBase = "ao hooks jcode "
	// maxSocketPath keeps the daemon's longest socket path under macOS's
	// 104-byte sun_path limit (Linux allows 108).
	maxSocketPath  = 100
	socketName     = "jcode.sock"
	longestSocket  = "jcode-debug.sock"
	stopTimeout    = 15 * time.Second
	runtimeDirMode = 0o700
)

// nativeHooks maps JCode's lifecycle hook env overrides onto the `ao hooks
// jcode <event>` callbacks. JCode has no prompt-submit or permission hook;
// turn_start fires when a submitted turn begins.
var nativeHooks = []struct{ env, config, event string }{
	{"JCODE_HOOK_SESSION_START", "session_start", "session-start"},
	{"JCODE_HOOK_TURN_START", "turn_start", "user-prompt-submit"},
	{"JCODE_HOOK_TURN_END", "turn_end", "stop"},
	{"JCODE_HOOK_SESSION_END", "session_end", "session-end"},
}

func overlayPath(workspace string) string {
	return filepath.Join(workspace, ".jcode", overlayFileName)
}

// runtimeDir is the per-session JCODE_RUNTIME_DIR. Its daemon is spawned by
// the session's own TUI, so hooks run with that session's AO environment. It
// lives under the AO data dir unless that path is too long for the daemon's
// sockets (sun_path), in which case it is a private directory in the OS temp
// dir, where AO keeps its own sockets too. Launch, hooks and Kill all derive
// the same path from the session id.
func runtimeDir(dataDir, sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	name := hex.EncodeToString(sum[:5])
	dir := filepath.Join(dataDir, "jc", name)
	if len(filepath.Join(dir, longestSocket)) <= maxSocketPath {
		return dir
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("ao-jcode-%d-%s", os.Getuid(), name))
}

// prepareRuntimeDir creates the session's daemon directory with mode 0700 and
// refuses one that is a symlink or not owned by this user.
func prepareRuntimeDir(dir string) error {
	if len(filepath.Join(dir, longestSocket)) > maxSocketPath {
		return fmt.Errorf("no directory short enough for the jcode daemon socket (%s)", dir)
	}
	if err := os.MkdirAll(dir, runtimeDirMode); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return fmt.Errorf("refusing jcode runtime dir %s: not a private directory", dir)
	}
	return os.Chmod(dir, runtimeDirMode)
}

// GetAgentHooks installs AO's instructions as the workspace's JCode prompt
// overlay and prepares the session's private daemon directory. Lifecycle
// hooks are passed per launch through AugmentRuntimeLaunchEnv.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("jcode.GetAgentHooks: WorkspacePath is required")
	}
	if strings.TrimSpace(cfg.DataDir) == "" || strings.TrimSpace(cfg.SessionID) == "" {
		return errors.New("jcode.GetAgentHooks: DataDir and SessionID are required")
	}
	if err := prepareRuntimeDir(runtimeDir(cfg.DataDir, cfg.SessionID)); err != nil {
		return fmt.Errorf("jcode.GetAgentHooks: runtime dir: %w", err)
	}
	instructions, err := systemPrompt(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return fmt.Errorf("jcode.GetAgentHooks: read system prompt: %w", err)
	}
	return writeOverlay(cfg.WorkspacePath, instructions)
}

func systemPrompt(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" || file == "" {
		return inline, nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // AO-owned launch config path
	return string(data), err
}

// writeOverlay owns .jcode/prompt-overlay.md only while its first line is AO's
// sentinel; a user's own overlay is never replaced. A symlinked .jcode
// directory or overlay file is refused so private instructions cannot land
// outside the workspace (for example in a global ~/.jcode).
func writeOverlay(workspace, instructions string) error {
	path := overlayPath(workspace)
	for _, candidate := range []string{filepath.Dir(path), path} {
		if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("jcode.GetAgentHooks: refusing to write instructions through symlink %s", candidate)
		}
	}
	data, err := os.ReadFile(path) //nolint:gosec // caller-owned workspace path, symlinks refused above
	switch {
	case err == nil && !strings.HasPrefix(string(data), overlaySentinel+"\n"):
		return fmt.Errorf("jcode.GetAgentHooks: refusing to overwrite non-AO prompt overlay at %s", path)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("jcode.GetAgentHooks: read prompt overlay: %w", err)
	}
	if strings.TrimSpace(instructions) == "" {
		if err == nil {
			return os.Remove(path)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("jcode.GetAgentHooks: create .jcode: %w", err)
	}
	body := overlaySentinel + "\n\n" + strings.TrimSpace(instructions) + "\n"
	if err := hookutil.AtomicWriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("jcode.GetAgentHooks: write prompt overlay: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(path), overlayFileName); err != nil {
		return fmt.Errorf("jcode.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

// AugmentRuntimeLaunchEnv gives the launch its own daemon directory, turns off
// JCode's self-update, and adds AO's hook callbacks after the user's own hooks
// for the same events (env overrides replace config hooks, so both are kept).
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, _ string) {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(string(sessionID)) == "" {
		return
	}
	dir := runtimeDir(dataDir, string(sessionID))
	// JCODE_SOCKET wins over JCODE_RUNTIME_DIR, so an inherited or project
	// value would route this session to another daemon; pin both.
	env["JCODE_RUNTIME_DIR"] = dir
	env["JCODE_SOCKET"] = filepath.Join(dir, socketName)
	env["JCODE_NO_AUTO_UPDATE"] = "1"
	env["JCODE_CHECK_UPDATES"] = "0"
	configured := configHooks(env)
	for _, hook := range nativeHooks {
		commands := userHookCommands(env, hook.env, configured[hook.config])
		env[hook.env] = tomlArray(append(commands, hookCommandBase+hook.event))
	}
}

// userHookCommands returns the user's commands for one event, from the
// environment override when set, else from config.toml, without AO's own.
func userHookCommands(env map[string]string, key string, configured any) []string {
	value, ok := env[key]
	if !ok {
		value, ok = os.LookupEnv(key)
	}
	var commands []string
	if ok {
		trimmed := strings.TrimSpace(value)
		var parsed struct {
			Commands any `toml:"commands"`
		}
		if strings.HasPrefix(trimmed, "[") && toml.Unmarshal([]byte("commands = "+trimmed), &parsed) == nil {
			commands = hookStrings(parsed.Commands)
		} else if trimmed != "" {
			commands = []string{trimmed}
		}
	} else {
		commands = hookStrings(configured)
	}
	kept := commands[:0]
	for _, command := range commands {
		if !strings.HasPrefix(strings.TrimSpace(command), hookCommandBase) {
			kept = append(kept, command)
		}
	}
	return kept
}

func hookStrings(value any) []string {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{v}
		}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// configHooks reads the [hooks] table of $JCODE_HOME/config.toml.
func configHooks(env map[string]string) map[string]any {
	home, err := jcodeHome(env)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml")) //nolint:gosec // JCode's own config path
	if err != nil {
		return nil
	}
	var config struct {
		Hooks map[string]any `toml:"hooks"`
	}
	if toml.Unmarshal(data, &config) != nil {
		return nil
	}
	return config.Hooks
}

func tomlArray(commands []string) string {
	data, err := toml.Marshal(map[string][]string{"commands": commands})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "commands ="))
}

// TerminateNativeSession stops this session's daemon so a killed session's
// running turn and tools stop with it. The saved transcript stays resumable.
func (p *Plugin) TerminateNativeSession(ctx context.Context, session ports.SessionRef) error {
	if strings.TrimSpace(session.DataDir) == "" || strings.TrimSpace(session.ID) == "" {
		return nil
	}
	dir := runtimeDir(session.DataDir, session.ID)
	socket := filepath.Join(dir, socketName)
	if _, err := os.Lstat(socket); err != nil {
		return nil
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return err
	}
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	cmd := process.CommandContext(stopCtx, binary, "--no-update", "server", "stop", "--force") //nolint:gosec // adapter-resolved binary, static args
	cmd.Env = append(os.Environ(), "JCODE_RUNTIME_DIR="+dir, "JCODE_SOCKET="+socket, "JCODE_NON_INTERACTIVE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jcode: stop session daemon: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeriveActivityState maps AO's JCode hook callbacks onto activity states.
// session_start fires when the TUI attaches (create, attach or resume), before
// any turn, so it reports idle.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "stop":
		return domain.ActivityIdle, true
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "session-end":
		return domain.ActivityExited, true
	default:
		return "", false
	}
}
