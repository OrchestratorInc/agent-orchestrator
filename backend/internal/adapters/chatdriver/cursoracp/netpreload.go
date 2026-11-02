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

// netPreload makes Cursor reuse its API connections: its CLI closes one after
// every call, and opening a session makes about a dozen. On a 225ms-RTT link it
// took a fresh process's first session/new from a 6.9s median to 5.0s.
//
//go:embed netpreload.cjs
var netPreload []byte

// writeNetPreload stores the preload under AO's data dir, named by its content
// so concurrent launches never write a file another process is reading.
func writeNetPreload(dataDir string) (string, error) {
	sum := sha256.Sum256(netPreload)
	path := filepath.Join(dataDir, "cursor", fmt.Sprintf("ao-net-%x.cjs", sum[:6]))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, netPreload) {
		return path, nil
	}
	// Node reads a preload only at process start, so files an older AO wrote
	// can go once this version's is in place.
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

// preloadPathUsable reports whether path survives NODE_OPTIONS parsing: a
// failed --require would stop Node before Cursor starts.
func preloadPathUsable(path string) bool {
	return !strings.ContainsAny(filepath.ToSlash(path), "\"\\\n")
}

// nodeOptionsWithPreload appends the preload, quoted and with forward slashes
// (which Node reads on every platform), to the NODE_OPTIONS the agent inherits.
func nodeOptionsWithPreload(existing, preload string) string {
	flag := `--require "` + filepath.ToSlash(preload) + `"`
	if strings.TrimSpace(existing) == "" {
		return flag
	}
	return strings.TrimSpace(existing) + " " + flag
}
