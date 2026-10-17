package minimaxcode

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

//go:embed assets/hook.cjs
var hookSource []byte

const profileMarker = "agent-orchestrator: private minimax-code profile\n"
const standingTransportMarker = "agent-orchestrator: native user-prompt context v1\n"
const maxStandingBytes = 512 << 10

// GetAgentHooks creates a durable private provider profile. MiniMax's TUI has
// no process-local permission flag, so the source profile is never edited.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := validateLaunch(ctx, cfg.Config.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return err
	}
	instructions := []byte(cfg.SystemPrompt)
	if cfg.SystemPromptFile != "" {
		file, err := os.Open(cfg.SystemPromptFile)
		if err != nil {
			return fmt.Errorf("MiniMax required standing instructions: %w", err)
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxStandingBytes {
			_ = file.Close()
			return errors.New("MiniMax standing instructions must be a bounded regular file")
		}
		instructions, err = io.ReadAll(io.LimitReader(file, maxStandingBytes+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if len(instructions) > maxStandingBytes {
		return errors.New("MiniMax standing instructions exceed the private context limit")
	}
	target, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return err
	}
	finalTarget := target
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	marker := filepath.Join(target, ".ao-managed")
	existing, err := os.ReadFile(marker)
	first := errors.Is(err, os.ErrNotExist)
	if err != nil && !first {
		return err
	}
	if first {
		if _, err := os.Lstat(target); err == nil {
			return errors.New("refusing to adopt a non-AO MiniMax profile")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		target, err = os.MkdirTemp(filepath.Dir(target), "."+cfg.SessionID+"-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(target) }()
		marker = filepath.Join(target, ".ao-managed")
	} else if string(existing) != profileMarker {
		return errors.New("MiniMax private profile ownership marker is invalid")
	} else if err := requireStandingTransport(target); err != nil {
		return err
	}
	var data []byte
	if first {
		source, err := sourceProfile(cfg.Env)
		if err != nil {
			return err
		}
		data, err = readPrivateConfig(filepath.Join(source, "config.yaml"))
		if err != nil {
			return err
		}
		// Preserve the profile's supported custom defaults and executable resources.
		// Native history/cache are deliberately per-AO-session and are not imported.
		for _, dir := range []string{"auth", "agents", "skills", "plugins", "memory", "harnesses", "subagents", "integrations", "AGENTS.md", "tui/keybindings.json"} {
			if err := copyProfileTree(ctx, filepath.Join(source, dir), filepath.Join(target, dir)); err != nil {
				return err
			}
		}
		if _, err := os.Lstat(filepath.Join(target, "plugins", "ao-activity")); err == nil {
			return errors.New("MiniMax source profile has a conflicting ao-activity plugin")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

	} else {
		source, err := sourceProfile(cfg.Env)
		if err != nil {
			return err
		}
		if err := refreshProfileCredentials(ctx, target, source); err != nil {
			return err
		}
		data, err = readPrivateConfig(filepath.Join(target, "config.yaml"))
		if err != nil {
			return err
		}
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return errors.New("MiniMax provider config is not valid YAML")
	}
	if config == nil {
		config = map[string]any{}
	}
	permissionMode := ports.NormalizePermissionMode(cfg.Config.Permissions)
	if permissionMode == ports.PermissionModeBypassPermissions {
		config["permissionMode"] = "bypassPermissions"
	} else {
		config["permissionMode"] = string(permissionMode)
	}
	data, err = yaml.Marshal(config)
	if err != nil {
		return errors.New("MiniMax provider config could not be encoded")
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(target, "config.yaml"), data, 0o600); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(marker, []byte(profileMarker), 0o600); err != nil {
		return err
	}
	if first {
		if err := hookutil.AtomicWriteFile(filepath.Join(target, "ao-standing-transport"), []byte(standingTransportMarker), 0o600); err != nil {
			return err
		}
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(target, "standing.txt"), instructions, 0o600); err != nil {
		return err
	}
	for _, dir := range []string{"tmp", "plugins/ao-activity/.claude-plugin", "plugins/ao-activity/hooks"} {
		if err := os.MkdirAll(filepath.Join(target, dir), 0o700); err != nil {
			return err
		}
	}
	plugin := filepath.Join(target, "plugins", "ao-activity")
	manifest := []byte(`{"name":"ao-activity","version":"0.0.1","description":"AO session activity and exact-identity guard"}`)
	if err := hookutil.AtomicWriteFile(filepath.Join(plugin, ".claude-plugin", "plugin.json"), manifest, 0o600); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(plugin, "hook.cjs"), hookSource, 0o600); err != nil {
		return err
	}
	if first {
		return os.Rename(target, finalTarget)
	}
	return nil
}
func sourceProfile(env map[string]string) (string, error) {
	for _, key := range []string{"MINIMAX_DATA_DIR", "MAVIS_DATA_DIR"} {
		if value := strings.TrimSpace(env[key]); value != "" {
			return filepath.Abs(value)
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return filepath.Abs(value)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".minimax"), nil
}
func readPrivateConfig(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}\n"), nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, errors.New("MiniMax config must be a bounded regular file")
	}
	return os.ReadFile(path)
}

// Refresh only source-owned credentials/configuration at each new native launch.
// Provider history and AO context files are never replaced. Validate and stage
// first so malformed, missing or symlinked auth cannot destroy a saved snapshot.
// A failed config write restores the old auth; failed rollback retains its backup.
func refreshProfileCredentials(ctx context.Context, target, source string) error {
	config, err := readPrivateConfig(filepath.Join(source, "config.yaml"))
	if err != nil {
		return err
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(config, &parsed); err != nil {
		return errors.New("MiniMax provider config is not valid YAML")
	}
	staging, err := os.MkdirTemp(target, ".ao-auth-refresh-")
	if err != nil {
		return err
	}
	keepBackup := false
	defer func() {
		if !keepBackup {
			_ = os.RemoveAll(staging)
		}
	}()
	stagedAuth := filepath.Join(staging, "auth")
	if err := copyProfileTree(ctx, filepath.Join(source, "auth"), stagedAuth); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	auth := filepath.Join(target, "auth")
	info, err := os.Lstat(stagedAuth)
	if errors.Is(err, os.ErrNotExist) {
		// An absent source is not an explicit credential replacement. Refuse
		// to pair its new config with an existing private auth snapshot.
		if _, err := os.Lstat(auth); err == nil {
			return errors.New("MiniMax source auth is missing; previous credentials and config were preserved")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return hookutil.AtomicWriteFile(filepath.Join(target, "config.yaml"), config, 0o600)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("MiniMax source auth must be a directory")
	}
	backup := filepath.Join(staging, "previous")
	err = os.Rename(auth, backup)
	hadAuth := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	installedAuth := false
	rollback := func(cause error) error {
		if installedAuth {
			if err := os.RemoveAll(auth); err != nil {
				keepBackup = true
				return errors.Join(cause, fmt.Errorf("remove replacement auth: %w; recovery staging retained at %s", err, staging))
			}
		}
		if hadAuth {
			if err := os.Rename(backup, auth); err != nil {
				keepBackup = true
				return errors.Join(cause, fmt.Errorf("restore previous auth: %w; snapshot retained at %s", err, backup))
			}
		}
		return cause
	}
	if err := os.Rename(stagedAuth, auth); err != nil {
		return rollback(err)
	}
	installedAuth = true
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(target, "config.yaml"), config, 0o600); err != nil {
		return rollback(err)
	}
	return nil
}

func copyProfileTree(ctx context.Context, source, target string) error {
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// Pin a source root so concurrent directory/symlink replacement cannot make
	// a snapshot read escape the selected resource's pinned parent directory.
	sourceRoot, err := os.OpenRoot(filepath.Dir(source))
	if err != nil {
		return err
	}
	defer func() { _ = sourceRoot.Close() }()
	count := 0
	var bytes int64
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("MiniMax profile snapshot refuses symlink %q; use a regular file or directory in the source profile", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		count++
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if count > 10000 || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return errors.New("MiniMax profile snapshot exceeds bounds")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		var b []byte
		if err := func() error {
			file, err := sourceRoot.Open(filepath.Join(filepath.Base(source), rel))
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()
			opened, err := file.Stat()
			if err != nil {
				return err
			}
			if !os.SameFile(info, opened) {
				return errors.New("MiniMax profile resource changed during snapshot")
			}
			b, err = io.ReadAll(io.LimitReader(file, (16<<20)+1))
			return err
		}(); err != nil {
			return err
		}
		bytes += int64(len(b))
		if len(b) > 16<<20 || bytes > 64<<20 {
			return errors.New("MiniMax profile snapshot exceeds bounds")
		}
		mode := fs.FileMode(0o600)
		if info.Mode()&0o100 != 0 {
			mode = 0o700
		}
		return hookutil.AtomicWriteFile(dst, b, mode)
	})
}

// CleanupWorkspace removes only a profile bearing AO's ownership marker after
// AO has removed its workspace. Kill/restore deliberately retain native history.
func (p *Plugin) CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	// The manager also calls this during failed preparation. Existing native
	// history must survive those retries and harness switches.
	if _, err := os.Lstat(cfg.WorkspacePath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return err
	}
	profiles := []struct {
		path, marker string
	}{
		{path, profileMarker},
		{filepath.Join(cfg.DataDir, "agents", adapterID, "chat", cfg.SessionID), chatProfileMarker},
	}
	for _, profile := range profiles {
		b, err := os.ReadFile(filepath.Join(profile.path, ".ao-managed"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if string(b) != profile.marker {
			return errors.New("refusing to remove non-AO MiniMax profile")
		}
		if err := os.RemoveAll(profile.path); err != nil {
			return err
		}
	}
	return nil
}

// PrepareRuntimeLaunch captures only AO's hook routing fields after the runtime
// generation is assigned. MiniMax intentionally strips AO_* from hook children.
func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	profile, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return err
	}
	env := map[string]string{}
	for _, key := range []string{"AO_SESSION_ID", "AO_DATA_DIR", "AO_RUN_FILE", "AO_RUNTIME_LAUNCH_ID"} {
		if value := cfg.Env[key]; value != "" {
			env[key] = value
		}
	}
	if env["AO_SESSION_ID"] != cfg.SessionID || env["AO_DATA_DIR"] != cfg.DataDir || !launchIDPattern.MatchString(env["AO_RUNTIME_LAUNCH_ID"]) {
		return errors.New("MiniMax hook routing requires the current AO runtime generation")
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	launch := filepath.Join(profile, "launches", env["AO_RUNTIME_LAUNCH_ID"])
	if err := os.MkdirAll(launch, 0o700); err != nil {
		return err
	}
	instructions, err := os.ReadFile(filepath.Join(profile, "standing.txt"))
	if err != nil {
		return fmt.Errorf("MiniMax private instruction snapshot: %w", err)
	}
	if len(instructions) > maxStandingBytes {
		return errors.New("MiniMax private instruction snapshot exceeds bounds")
	}
	f, err := os.OpenFile(filepath.Join(launch, "routing.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("MiniMax immutable hook generation: %w", err)
	}
	_, writeErr := f.Write(body)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(launch, "standing.txt"), instructions, 0o600); err != nil {
		return err
	}
	if err := writeHookConfig(filepath.Join(profile, "plugins", "ao-activity"), launch); err != nil {
		return err
	}
	// Publish the complete current generation before pruning. A cached hook
	// from an earlier generation then fails closed on its missing routing file.
	root, err := os.OpenRoot(profile)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	marker, err := fs.ReadFile(root.FS(), ".ao-managed")
	if err != nil || string(marker) != profileMarker {
		return errors.New("MiniMax private profile ownership marker is invalid")
	}
	entries, err := fs.ReadDir(root.FS(), "launches")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == env["AO_RUNTIME_LAUNCH_ID"] || !entry.IsDir() || !launchIDPattern.MatchString(entry.Name()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.RemoveAll(filepath.Join("launches", entry.Name())); err != nil {
			return fmt.Errorf("prune MiniMax launch %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func requireStandingTransport(profile string) error {
	marker, err := os.ReadFile(filepath.Join(profile, "ao-standing-transport"))
	if err != nil || string(marker) != standingTransportMarker {
		return errors.New("MiniMax legacy identity-append history cannot refresh AO standing instructions; preserve that history and start a new AO conversation")
	}
	return nil
}

func writeHookConfig(plugin, launch string) error {
	// MiniMax expands its own plugin-root variable before invoking the command.
	// Its cached command pins the generation without repurposing TMPDIR. Encode
	// the absolute path so spaces and shell metacharacters remain a single arg.
	command := `node "${CLAUDE_PLUGIN_ROOT}/hook.cjs" ` + base64.RawURLEncoding.EncodeToString([]byte(launch))

	hooks := map[string]any{}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "Stop"} {
		hooks[ev] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}}}}
	}
	body, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(filepath.Join(plugin, "hooks", "hooks.json"), body, 0o600)

}
