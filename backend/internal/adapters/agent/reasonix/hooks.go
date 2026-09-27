package reasonix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const reasonixHookOwner = "agent-orchestrator: managed Reasonix observer"

var reasonixHookEvents = []struct{ native, ao string }{
	{"SessionStart", "session-start"}, {"UserPromptSubmit", "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"}, {"PostToolUse", "post-tool-use"},
	{"PermissionRequest", "permission-request"}, {"Stop", "stop"}, {"SessionEnd", "session-end"},
}

type reasonixSettings struct {
	path     string
	original []byte
	info     os.FileInfo
	top      map[string]json.RawMessage
	hooks    map[string][]json.RawMessage
}

// GetAgentHooks appends native direct-array observer hooks after user hooks,
// preserving their order and all unknown settings. A lock serializes AO writers;
// a snapshot check also refuses files replaced or edited by another writer.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return updateReasonixHooks(ctx, cfg.WorkspacePath, true)
}

// UninstallHooks removes only entries bearing AO's ownership marker.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return updateReasonixHooks(ctx, workspacePath, false)
}

// AreHooksInstalled reports whether an AO-owned callback remains in settings.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, err := reasonixSettingsPath(ctx, workspacePath)
	if err != nil {
		return false, err
	}
	settings, err := readReasonixSettings(ctx, path)
	if err != nil {
		return false, err
	}
	for _, entries := range settings.hooks {
		for _, entry := range entries {
			if reasonixOwnedHook(entry) {
				return true, nil
			}
		}
	}
	return false, nil
}

func reasonixSettingsPath(ctx context.Context, workspacePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return "", errors.New("reasonix hooks: workspace path is required")
	}
	path, err := filepath.Abs(filepath.Join(workspacePath, ".reasonix", "settings.json"))
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(filepath.Dir(path)); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("reasonix hooks: settings directory must be a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func readReasonixSettings(ctx context.Context, path string) (*reasonixSettings, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &reasonixSettings{path: path, top: map[string]json.RawMessage{}, hooks: map[string][]json.RawMessage{}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("reasonix hooks: settings must be a regular file")
	}
	s.info = info
	s.original, err = os.ReadFile(path) //nolint:gosec // Caller-owned workspace settings.
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(s.original, &s.top); err != nil {
		return nil, fmt.Errorf("reasonix hooks: parse settings: %w", err)
	}
	if s.top == nil {
		return nil, errors.New("reasonix hooks: settings must be an object")
	}
	if raw, ok := s.top["hooks"]; ok {
		if err := json.Unmarshal(raw, &s.hooks); err != nil {
			return nil, fmt.Errorf("reasonix hooks: parse hooks: %w", err)
		}
		if s.hooks == nil {
			return nil, errors.New("reasonix hooks: hooks must be an object")
		}
	}
	for event, entries := range s.hooks {
		if entries == nil {
			return nil, fmt.Errorf("reasonix hooks: %s must be an array", event)
		}
		for _, entry := range entries {
			var fields map[string]json.RawMessage
			var native struct {
				Command     string `json:"command"`
				Match       string `json:"match"`
				Description string `json:"description"`
				Timeout     int    `json:"timeout"`
			}
			if json.Unmarshal(entry, &fields) != nil || fields == nil || json.Unmarshal(entry, &native) != nil || strings.TrimSpace(native.Command) == "" {
				return nil, fmt.Errorf("reasonix hooks: malformed %s entry", event)
			}
		}
	}
	if err := s.unchanged(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func reasonixOwnedHook(entry json.RawMessage) bool {
	var native struct {
		Description string `json:"description"`
	}
	return json.Unmarshal(entry, &native) == nil && native.Description == reasonixHookOwner
}

func updateReasonixHooks(ctx context.Context, workspacePath string, install bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := reasonixSettingsPath(ctx, workspacePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if !install {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, ".ao-hooks.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // Exclusive AO writer lock in workspace.
	if err != nil {
		return fmt.Errorf("reasonix hooks: acquire settings lock: %w", err)
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lockPath) }()
	settings, err := readReasonixSettings(ctx, path)
	if err != nil {
		return err
	}
	for event, entries := range settings.hooks {
		kept := make([]json.RawMessage, 0, len(entries))
		for _, entry := range entries {
			if !reasonixOwnedHook(entry) {
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 && len(entries) > 0 {
			delete(settings.hooks, event)
		} else {
			settings.hooks[event] = kept
		}
	}
	if install {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		for _, spec := range reasonixHookEvents {
			entry, err := json.Marshal(struct {
				Command     string `json:"command"`
				Description string `json:"description"`
				Timeout     int    `json:"timeout"`
			}{reasonixObserverCommand(executable, spec.ao), reasonixHookOwner, 10000})
			if err != nil {
				return err
			}
			settings.hooks[spec.native] = append(settings.hooks[spec.native], entry)
		}
	}
	if len(settings.hooks) == 0 {
		delete(settings.top, "hooks")
	} else {
		raw, err := json.Marshal(settings.hooks)
		if err != nil {
			return err
		}
		settings.top["hooks"] = raw
	}
	data, err := json.MarshalIndent(settings.top, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := settings.write(ctx, data); err != nil {
		return err
	}
	if install {
		return hookutil.EnsureWorkspaceGitignore(dir, "settings.json")
	}
	return nil
}

func (s *reasonixSettings) unchanged(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(s.path)
	if s.info == nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.New("reasonix hooks: settings appeared during update")
	}
	if err != nil {
		return fmt.Errorf("reasonix hooks: settings changed during update: %w", err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(s.info, current) || s.info.Mode() != current.Mode() {
		return errors.New("reasonix hooks: settings replaced during update")
	}
	data, err := os.ReadFile(s.path) //nolint:gosec // Recheck the exact workspace file before replacement.
	if err != nil {
		return err
	}
	if !bytes.Equal(data, s.original) {
		return errors.New("reasonix hooks: settings edited during update")
	}
	return nil
}

func (s *reasonixSettings) write(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if bytes.Equal(data, s.original) {
		return s.unchanged(ctx)
	}
	perm := os.FileMode(0o600)
	if s.info != nil {
		perm = s.info.Mode().Perm()
	}
	// Stage with the shared fsync+rename writer, then revalidate immediately before
	// publication. Unrelated writers do not participate in AO's exclusive lock.
	staged, err := os.CreateTemp(filepath.Dir(s.path), ".ao-reasonix-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()
	if err := staged.Close(); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(stagedPath, data, perm); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.unchanged(ctx); err != nil {
		return err
	}
	return os.Rename(stagedPath, s.path)
}

func reasonixObserverCommand(executable, event string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(executable, `"`, `""`) + `" hooks reasonix ` + event + ` >NUL 2>&1 & exit /b 0`
	}
	quoted := "'" + strings.ReplaceAll(executable, "'", `'"'"'`) + "'"
	return quoted + " hooks reasonix " + event + " >/dev/null 2>&1 || true"
}
