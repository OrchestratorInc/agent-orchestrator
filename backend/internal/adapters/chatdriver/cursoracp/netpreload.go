package cursoracp

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

//go:embed netpreload.cjs
var netPreload []byte

func writeNetPreload(dataDir string) (string, error) {
	sum := sha256.Sum256(netPreload)
	path := filepath.Join(dataDir, "cursor", fmt.Sprintf("ao-net-%x.cjs", sum[:6]))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, netPreload) {
		return path, nil
	}
	defer removeStaleNetPreloads(filepath.Dir(path), filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create Cursor preload directory: %w", err)
	}
	if err := hookutil.AtomicWriteFile(path, netPreload, 0o600); err != nil {
		return "", fmt.Errorf("write Cursor preload: %w", err)
	}
	return path, nil
}

func removeStaleNetPreloads(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != keep && strings.HasPrefix(name, "ao-net-") && strings.HasSuffix(name, ".cjs") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func preloadPathUsable(path string) bool {
	return !strings.ContainsAny(filepath.ToSlash(path), "\"\\\n")
}

func nodeOptionsWithPreload(existing, preload string) string {
	flag := `--require "` + filepath.ToSlash(preload) + `"`
	if strings.TrimSpace(existing) == "" {
		return flag
	}
	return strings.TrimSpace(existing) + " " + flag
}
