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
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

const copilotTrustedFoldersKey = "trustedFolders"

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

var copilotTrustMu sync.Mutex

func ensureCopilotFolderTrusted(configPath, workspacePath string) error {
	copilotTrustMu.Lock()
	defer copilotTrustMu.Unlock()
	if target, err := filepath.EvalSymlinks(configPath); err == nil {
		configPath = target
	}
	folder, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("copilot: resolve workspace: %w", err)
	}
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
