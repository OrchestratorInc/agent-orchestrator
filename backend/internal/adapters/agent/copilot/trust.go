package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// Copilot CLI shows an interactive "Confirm folder trust" dialog on first
// launch in an untrusted directory. That dialog would swallow input injected
// into the TUI, so AO pre-records trust the same way Copilot does when the user
// picks "Yes, and remember this folder for future sessions".
//
// Trust layout (verified against Copilot CLI 1.0.93): the absolute,
// symlink-resolved folder path is appended to the "trustedFolders" array in
// $COPILOT_HOME/config.json (default ~/.copilot/config.json). Copilot manages
// that file itself and prefixes it with "//" comment lines, which are kept.
const copilotTrustedFoldersKey = "trustedFolders"

// EnsureWorkspaceTrusted records workspacePath as trusted in the Copilot home
// the current process environment points at (COPILOT_HOME, else ~/.copilot).
// The daemon-owned authentication terminal runs Copilot against that real home
// so login credentials land where the user's own Copilot reads them; seeding
// trust there keeps the trust dialog from blocking the injected /login.
func EnsureWorkspaceTrusted(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil //nolint:nilerr // Copilot can still ask for folder trust interactively.
	}
	return ensureCopilotFolderTrusted(filepath.Join(copilotHomeDir(home), "config.json"), workspacePath)
}

func ensureCopilotFolderTrusted(configPath, workspacePath string) error {
	folder, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("copilot: resolve workspace: %w", err)
	}
	// Copilot compares against the real path (macOS /tmp is /private/tmp).
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}

	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("copilot: read %s: %w", configPath, err)
	}
	header, body := splitCopilotConfigHeader(data)
	config := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(body)) > 0 {
		// Never rewrite a config AO cannot round-trip; Copilot then just asks
		// for folder trust interactively. A top-level null decodes without
		// error but leaves config nil, so it is rejected the same way.
		if err := json.Unmarshal(body, &config); err != nil || config == nil {
			return nil //nolint:nilerr // Leave unsupported config untouched; Copilot owns the fallback prompt.
		}
	}
	var folders []string
	if raw, ok := config[copilotTrustedFoldersKey]; ok {
		if err := json.Unmarshal(raw, &folders); err != nil {
			return nil //nolint:nilerr // Leave an unsupported trust shape untouched.
		}
	}
	for _, existing := range folders {
		if filepath.Clean(existing) == folder {
			return nil
		}
	}
	encoded, err := json.Marshal(append(folders, folder))
	if err != nil {
		return fmt.Errorf("copilot: encode %s: %w", copilotTrustedFoldersKey, err)
	}
	config[copilotTrustedFoldersKey] = encoded
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("copilot: encode config: %w", err)
	}
	if len(header) > 0 && header[len(header)-1] != '\n' {
		header = append(header, '\n')
	}
	out = append(append(header, out...), '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("copilot: create config dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(configPath, out, 0o600); err != nil {
		return fmt.Errorf("copilot: write %s: %w", configPath, err)
	}
	return nil
}

// splitCopilotConfigHeader separates the leading "//" comment lines Copilot
// writes at the top of config.json from the JSON document that follows.
func splitCopilotConfigHeader(data []byte) (header, body []byte) {
	rest := data
	for len(rest) > 0 {
		line := rest
		next := []byte(nil)
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, next = rest[:i+1], rest[i+1:]
		}
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			break
		}
		header = append(header, line...)
		rest = next
	}
	return header, rest
}
