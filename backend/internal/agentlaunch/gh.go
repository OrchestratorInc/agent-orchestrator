package agentlaunch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const ghWrapperMarker = ".ao-gh-wrapper"

// RealGH resolves the next executable on the agent's effective PATH, skipping
// AO-owned wrapper directories (including those inherited from older launches).
func RealGH(path string) (string, error) {
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ghWrapperMarker)); err == nil {
			continue
		}
		candidate := filepath.Join(dir, name)
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("ao gh: real gh executable not found on PATH")
}

// GHPath removes our gh aliases from the delegated process's PATH. Third-party
// wrappers often delegate through PATH themselves. Retain ao in a directory
// without gh so those wrappers cannot bounce back into our interception layer.
func GHPath(path string) string {
	parts := filepath.SplitList(path)
	for i, dir := range parts {
		if _, err := os.Stat(filepath.Join(dir, ghWrapperMarker)); err == nil {
			parts[i] = filepath.Join(dir, "passthrough")
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}
