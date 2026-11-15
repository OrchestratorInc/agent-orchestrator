package binaryutil

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

var managedBinDir atomic.Pointer[string]

func SetManagedBinDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		managedBinDir.Store(nil)
		return
	}
	managedBinDir.Store(&dir)
}

func ManagedBinary(names ...string) (string, bool) {
	dir := managedBinDir.Load()
	if dir == nil {
		return "", false
	}
	for _, name := range names {
		for _, candidate := range managedCandidates(*dir, name) {
			if hookutil.IsExecutableFile(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

func ManagedCurrentDir(launcher, target string) (string, bool) {
	dir := managedBinDir.Load()
	if dir == nil || launcher == "" || target == "" {
		return "", false
	}
	launcherDir := filepath.Clean(filepath.Dir(launcher))
	binDir := filepath.Clean(*dir)
	if launcherDir != binDir && (runtime.GOOS != "windows" || !strings.EqualFold(launcherDir, binDir)) {
		return "", false
	}
	return filepath.Join(filepath.Dir(binDir), target, "current"), true
}

func managedCandidates(dir, name string) []string {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return nil
	}
	if runtime.GOOS != "windows" {
		return []string{filepath.Join(dir, name)}
	}
	base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".cmd"), ".bat")
	return []string{filepath.Join(dir, base+".cmd")}
}
