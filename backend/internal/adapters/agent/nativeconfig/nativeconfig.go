// Package nativeconfig resolves provider-owned session state directories.
package nativeconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// codexRollout is how Codex names a thread's rollout: `rollout-<time>-<thread>`,
// compressed to .jsonl.zst once inactive, and continued in `_<segment>` files.
var codexRollout = regexp.MustCompile(`^rollout-.*-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(?:_[0-9a-fA-F-]{36})?\.jsonl(?:\.zst)?$`)

// CodexRolloutThread is the thread a Codex rollout file belongs to, or "" when
// the name is not a rollout. Resume probes and history imports share it, so
// they always agree on which files make up a thread.
func CodexRolloutThread(path string) string {
	if m := codexRollout.FindStringSubmatch(filepath.Base(path)); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// Resolve returns the directory selected for a provider invocation. An entry
// in env, including an explicitly empty one, takes precedence over the daemon
// environment because env describes the child process being launched.
func Resolve(env map[string]string, envKey, defaultDir string) (string, error) {
	if value, ok := env[envKey]; ok {
		if dir := strings.TrimSpace(value); dir != "" {
			return filepath.Clean(dir), nil
		}
		return homeDefault(env, defaultDir)
	}
	if dir := strings.TrimSpace(os.Getenv(envKey)); dir != "" {
		return filepath.Clean(dir), nil
	}
	return homeDefault(env, defaultDir)
}

func homeDefault(env map[string]string, defaultDir string) (string, error) {
	if home := strings.TrimSpace(env["HOME"]); home != "" {
		return filepath.Join(home, defaultDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultDir), nil
}
