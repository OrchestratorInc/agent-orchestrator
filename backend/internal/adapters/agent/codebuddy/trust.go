package codebuddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// CodeBuddy asks "Do you trust the files in this folder?" the first time it
// starts in a directory and records the answer in its settings.json under
// trustedDirectories. In AO's private login workspace that dialog would take
// the /login AO sends, so the workspace is recorded as trusted first.
const trustedDirectoriesKey = "trustedDirectories"

var trustMu sync.Mutex

// EnsureWorkspaceTrusted records workspacePath in CodeBuddy's trusted
// directories (CODEBUDDY_CONFIG_DIR, else ~/.codebuddy). Settings AO cannot
// round-trip are left untouched; CodeBuddy then asks interactively.
func EnsureWorkspaceTrusted(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	dir := strings.TrimSpace(os.Getenv("CODEBUDDY_CONFIG_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return nil //nolint:nilerr // CodeBuddy can still ask for folder trust interactively.
		}
		dir = filepath.Join(home, ".codebuddy")
	}
	return ensureTrusted(filepath.Join(dir, "settings.json"), workspacePath)
}

func ensureTrusted(settingsPath, workspacePath string) error {
	trustMu.Lock()
	defer trustMu.Unlock()
	// A settings file linked from a dotfiles repo is updated there, not replaced.
	if target, err := filepath.EvalSymlinks(settingsPath); err == nil {
		settingsPath = target
	}
	folder, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("codebuddy: resolve workspace: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("codebuddy: read %s: %w", settingsPath, err)
	}
	settings := map[string]json.RawMessage{}
	if strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
			return nil //nolint:nilerr // Leave unsupported settings untouched; CodeBuddy owns the fallback prompt.
		}
	}
	var dirs []string
	if raw, ok := settings[trustedDirectoriesKey]; ok {
		if err := json.Unmarshal(raw, &dirs); err != nil {
			return nil //nolint:nilerr // Leave an unsupported trust shape untouched.
		}
	}
	for _, existing := range dirs {
		if filepath.Clean(existing) == folder {
			return nil
		}
	}
	encoded, err := json.Marshal(append(dirs, folder))
	if err != nil {
		return fmt.Errorf("codebuddy: encode %s: %w", trustedDirectoriesKey, err)
	}
	settings[trustedDirectoriesKey] = encoded
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("codebuddy: encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return fmt.Errorf("codebuddy: create settings dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(settingsPath, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("codebuddy: write %s: %w", settingsPath, err)
	}
	return nil
}
