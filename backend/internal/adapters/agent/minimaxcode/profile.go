package minimaxcode

import (
	"context"
	_ "embed"
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

// GetAgentHooks creates a durable private provider profile. MiniMax's TUI has
// no process-local permission flag, so the source profile is never edited.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := validateLaunch(ctx, cfg.Config.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return err
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
	config["permissionMode"] = string(ports.NormalizePermissionMode(cfg.Config.Permissions))
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
	if err := writeHookConfig(plugin); err != nil {
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
			return errors.New("MiniMax profile snapshot refuses symlinks")
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
		b, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		if err != nil {
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
	b, err := os.ReadFile(filepath.Join(path, ".ao-managed"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(b) != profileMarker {
		return fmt.Errorf("refusing to remove non-AO MiniMax profile")
	}
	return os.RemoveAll(path)
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
	if env["AO_SESSION_ID"] != cfg.SessionID || env["AO_DATA_DIR"] != cfg.DataDir || !aoIDPattern.MatchString(env["AO_RUNTIME_LAUNCH_ID"]) {
		return errors.New("MiniMax hook routing requires the current AO runtime generation")
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	launch := filepath.Join(profile, "launches", env["AO_RUNTIME_LAUNCH_ID"])
	if err := os.MkdirAll(filepath.Join(launch, "tmp"), 0o700); err != nil {
		return err
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
	return closeErr
}

func writeHookConfig(plugin string) error {
	// MiniMax expands its own plugin-root variable before invoking the command.
	command := `node "${CLAUDE_PLUGIN_ROOT}/hook.cjs"`

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
