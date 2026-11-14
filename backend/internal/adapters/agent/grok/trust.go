package grok

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	grokTrustedFoldersFile = "trusted_folders.toml"
	grokTrustedFoldersKey  = "folders"
)

var grokTrustMu sync.Mutex

func (p *Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return nil
	}
	home, err := p.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil || strings.TrimSpace(home) == "" {
		return nil //nolint:nilerr // Grok can still ask for folder trust interactively.
	}
	return ensureGrokFolderTrusted(filepath.Join(home, grokTrustedFoldersFile), cfg.WorkspacePath, time.Now())
}

func ensureGrokFolderTrusted(trustPath, workspacePath string, now time.Time) error {
	grokTrustMu.Lock()
	defer grokTrustMu.Unlock()
	if target, err := filepath.EvalSymlinks(trustPath); err == nil {
		trustPath = target
	} else if link, lerr := os.Readlink(trustPath); lerr == nil {
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(trustPath), link)
		}
		trustPath = link
	}
	folder, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("grok: resolve workspace: %w", err)
	}
	folders := []string{folder}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil && !sameGrokFolder(resolved, folder) {
		folders = append(folders, resolved)
	}

	data, err := os.ReadFile(trustPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("grok: read %s: %w", trustPath, err)
	}
	existing, ok := grokTrustedFolderKeys(data)
	if !ok {
		return nil
	}

	var block bytes.Buffer
	for _, f := range folders {
		if grokFolderKnown(existing, f) {
			continue
		}
		key, ok := grokTOMLKey(f)
		if !ok {
			continue
		}
		fmt.Fprintf(&block, "[%s.%s]\ntrusted = true\ndecided_at = %d\n\n", grokTrustedFoldersKey, key, now.Unix())
	}
	if block.Len() == 0 {
		return nil
	}

	out := append([]byte(nil), data...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if len(bytes.TrimSpace(out)) > 0 {
		out = append(out, '\n')
	}
	out = append(out, bytes.TrimRight(block.Bytes(), "\n")...)
	out = append(out, '\n')
	if keys, ok := grokTrustedFolderKeys(out); !ok || !grokFolderKnown(keys, folder) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(trustPath), 0o700); err != nil {
		return fmt.Errorf("grok: create trust dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(trustPath, out, 0o600); err != nil {
		return fmt.Errorf("grok: write %s: %w", trustPath, err)
	}
	return nil
}

func grokTrustedFolderKeys(data []byte) (map[string]any, bool) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, true
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil || doc == nil {
		return nil, false
	}
	raw, present := doc[grokTrustedFoldersKey]
	if !present {
		return map[string]any{}, true
	}
	folders, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return folders, true
}

func grokFolderKnown(folders map[string]any, folder string) bool {
	for key := range folders {
		if sameGrokFolder(key, folder) {
			return true
		}
	}
	return false
}

func sameGrokFolder(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func grokTOMLKey(folder string) (string, bool) {
	for _, r := range folder {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	if !strings.Contains(folder, "'") {
		return "'" + folder + "'", true
	}
	return strconv.Quote(folder), true
}
