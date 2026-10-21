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
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
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
		for _, dir := range []string{"auth", "agents", "skills", "plugins", "memory", "harnesses", "subagents", "integrations", "AGENTS.md", "permission.json", nativeRegionPreference, "tui/keybindings.json"} {
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

// nativeRegionPreference records the account namespace (Global or China)
// MiniMax uses when auth state exists for both; without it the native
// resolver can pick the other namespace.
const nativeRegionPreference = "preferences/mcode-region.json"

// Refresh only source-owned credentials/configuration at each new native launch.
// Provider history and AO context files are never replaced. Validate and stage
// first so malformed, missing or symlinked auth cannot destroy a saved snapshot.
// A failed later step restores the old auth and region preference; failed
// rollback retains its backup.
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
	// The region preference selects which auth namespace is active, so it is
	// staged with auth and published or restored together with it. Like auth,
	// a preference absent from the source leaves any private one intact.
	stagedPreference := filepath.Join(staging, "region.json")
	if err := copyProfileTree(ctx, filepath.Join(source, filepath.FromSlash(nativeRegionPreference)), stagedPreference); err != nil {
		return err
	}
	stagedAuth := filepath.Join(staging, "auth")
	if err := copyProfileTree(ctx, filepath.Join(source, "auth"), stagedAuth); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(stagedAuth)
	hasSourceAuth := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hasSourceAuth && !info.IsDir() {
		return errors.New("MiniMax source auth must be a directory")
	}
	var undo []func() error
	rollback := func(cause error) error {
		for i := len(undo) - 1; i >= 0; i-- {
			if err := undo[i](); err != nil {
				keepBackup = true
				return errors.Join(cause, fmt.Errorf("%w; recovery staging retained at %s", err, staging))
			}
		}
		return cause
	}
	// replace swaps staged into live, keeping any previous copy for rollback.
	replace := func(staged, live, backup, name string) error {
		if err := os.MkdirAll(filepath.Dir(live), 0o700); err != nil {
			return err
		}
		err := os.Rename(live, backup)
		hadPrevious := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		restore := func() error {
			if hadPrevious {
				if err := os.Rename(backup, live); err != nil {
					return fmt.Errorf("restore previous %s: %w", name, err)
				}
			}
			return nil
		}
		if err := os.Rename(staged, live); err != nil {
			if restoreErr := restore(); restoreErr != nil {
				keepBackup = true
				return errors.Join(err, restoreErr)
			}
			return err
		}
		undo = append(undo, func() error {
			if err := os.RemoveAll(live); err != nil {
				return fmt.Errorf("remove replacement %s: %w", name, err)
			}
			return restore()
		})
		return nil
	}
	// Config-only providers need no source auth directory. MiniMax may create
	// private auth state at runtime; leave it intact on restore.
	if hasSourceAuth {
		if err := replace(stagedAuth, filepath.Join(target, "auth"), filepath.Join(staging, "previous"), "auth"); err != nil {
			return rollback(err)
		}
	}
	if _, err := os.Lstat(stagedPreference); err == nil {
		if err := replace(stagedPreference, filepath.Join(target, filepath.FromSlash(nativeRegionPreference)), filepath.Join(staging, "previous-region.json"), "region preference"); err != nil {
			return rollback(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(target, "config.yaml"), config, 0o600); err != nil {
		return rollback(err)
	}
	return nil
}

// Skill installers commonly link one shared copy of a skill into each agent's
// profile, so these resource trees snapshot the content behind symlinked
// entries. Credentials, policy and instruction files keep refusing links.
var linkedProfileResources = map[string]bool{"skills": true, "agents": true, "subagents": true, "plugins": true}

const maxProfileLinkDepth = 8

type profileSnapshot struct {
	followLinks bool
	count       int
	bytes       int64
}

func copyProfileTree(ctx context.Context, source, target string) error {
	snapshot := &profileSnapshot{followLinks: linkedProfileResources[filepath.Base(source)]}
	return snapshot.copyTree(ctx, source, target, 0)
}

// copyLink snapshots a followed symlink's resolved content at target. Dangling
// links contribute nothing; link chains and cycles are bounded by depth.
func (s *profileSnapshot) copyLink(ctx context.Context, link, target string, depth int) error {
	if depth >= maxProfileLinkDepth {
		return fmt.Errorf("MiniMax profile snapshot symlink %q nests too deeply", link)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return s.copyTree(ctx, resolved, target, depth+1)
}

func (s *profileSnapshot) copyTree(ctx context.Context, source, target string, depth int) error {
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
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			if !s.followLinks {
				return fmt.Errorf("MiniMax profile snapshot refuses symlink %q; use a regular file or directory in the source profile", path)
			}
			return s.copyLink(ctx, path, dst, depth)
		}
		if entry.IsDir() {
			// MiniMax's cross-process locks are <file>.lock directories. A copy
			// has no owner in the private profile, so native code would wait
			// for it to go stale instead of acquiring it.
			if path != source && strings.HasSuffix(entry.Name(), ".lock") {
				return fs.SkipDir
			}
			return os.MkdirAll(dst, 0o700)
		}
		s.count++
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if s.count > 10000 || !info.Mode().IsRegular() || info.Size() > 16<<20 {
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
		s.bytes += int64(len(b))
		if len(b) > 16<<20 || s.bytes > 64<<20 {
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
	if err := writeHookConfig(filepath.Join(profile, "plugins", "ao-activity"), hookNode(ctx), launch); err != nil {
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

// hookNode returns the hook interpreter. MiniMax's official installer runs
// a private Node runtime by absolute path without putting it on PATH, so
// hooks name that runtime; other installs rely on PATH's node.
var hookNode = func(ctx context.Context) string {
	binary, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err != nil {
		return "node"
	}
	if node := officialLauncherNode(binary); node != "" {
		return node
	}
	return "node"
}

var releasePattern = regexp.MustCompile(`^[0-9A-Za-z._-]+$`)

// officialLauncherNode reads the Node runtime pinned by the official
// installer. On POSIX, <root>/bin/mcode execs <root>/releases/<current>/
// .mcode-launcher, which assigns node=<shell word>. On Windows, <root>/mcode.cmd
// calls releases/<current>/.mcode-launcher.cmd, which invokes "<node.exe>"
// directly. It returns "" for any other install.
func officialLauncherNode(binary string) string {
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return ""
	}
	windows := strings.EqualFold(filepath.Base(resolved), "mcode.cmd")
	root, launcherName := filepath.Dir(filepath.Dir(resolved)), ".mcode-launcher"
	if windows {
		root, launcherName = filepath.Dir(resolved), ".mcode-launcher.cmd"
	}
	current, err := os.ReadFile(filepath.Join(root, "current"))
	if err != nil {
		return ""
	}
	release := strings.TrimSpace(string(current))
	if !releasePattern.MatchString(release) || release == "." || release == ".." {
		return ""
	}
	launcher, err := os.ReadFile(filepath.Join(root, "releases", release, launcherName))
	if err != nil || len(launcher) > 64<<10 {
		return ""
	}
	node := ""
	for _, line := range strings.Split(string(launcher), "\n") {
		line = strings.TrimSpace(line)
		if windows {
			// The runtime is the first quoted word. install.ps1 writes a literal
			// percent as %%; any other percent is a cmd variable, not resolved.
			if rest, ok := strings.CutPrefix(line, `"`); ok {
				if end := strings.IndexByte(rest, '"'); end > 0 {
					word := strings.ReplaceAll(rest[:end], "%%", "\x00")
					if !strings.Contains(word, "%") {
						node = strings.ReplaceAll(word, "\x00", "%")
					}
				}
				break
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "node="); ok {
			node, _ = unquoteShellWord(value)
			break
		}
	}
	if node == "" || !filepath.IsAbs(node) {
		return ""
	}
	info, err := os.Stat(node)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if windows {
		if !strings.EqualFold(filepath.Ext(node), ".exe") {
			return ""
		}
	} else if info.Mode()&0o111 == 0 {
		return ""
	}
	return node
}

// unquoteShellWord decodes one POSIX shell word made of literal, single- and
// double-quoted parts, which covers install.sh's '...'"'"'...' escaping.
// Expansions and operators are rejected rather than guessed.
func unquoteShellWord(word string) (string, bool) {
	if strings.HasPrefix(word, "~") {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(word); {
		switch c := word[i]; c {
		case '\'':
			end := strings.IndexByte(word[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(word[i+1 : i+1+end])
			i += end + 2
		case '"':
			i++
			for {
				if i >= len(word) || word[i] == '$' || word[i] == '`' {
					return "", false
				}
				if word[i] == '"' {
					i++
					break
				}
				if word[i] == '\\' && i+1 < len(word) && strings.IndexByte("$`\"\\", word[i+1]) >= 0 {
					i++
				}
				b.WriteByte(word[i])
				i++
			}
		case '\\':
			if i+1 >= len(word) {
				return "", false
			}
			b.WriteByte(word[i+1])
			i += 2
		case '$', '`', ' ', '\t', ';', '&', '|', '<', '>', '(', ')', '*', '?', '[', '#':
			return "", false
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), true
}

func writeHookConfig(plugin, node, launch string) error {
	// MiniMax expands its own plugin-root variable before invoking the command.
	// Its cached command pins the generation without repurposing TMPDIR.
	argument := base64.RawURLEncoding.EncodeToString([]byte(launch))
	handler := map[string]any{"type": "command", "command": `node "${CLAUDE_PLUGIN_ROOT}/hook.cjs" ` + argument, "timeout": 10}
	if node != "node" {
		// A pinned runtime path is spawned directly with argv: no shell, so no
		// quoting that differs between sh, Git Bash and PowerShell.
		handler = map[string]any{"type": "command", "command": node, "args": []string{"${CLAUDE_PLUGIN_ROOT}/hook.cjs", argument}, "timeout": 10}
	}

	hooks := map[string]any{}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "Stop"} {
		hooks[ev] = []any{map[string]any{"hooks": []any{handler}}}
	}
	body, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(filepath.Join(plugin, "hooks", "hooks.json"), body, 0o600)

}
