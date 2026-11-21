// Package nativeconfig resolves provider-owned session state directories.
package nativeconfig

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

// FindFile walks root for the first regular file whose name match accepts. A
// missing root is not found. One unreadable folder below root does not hide the
// file, as discovery skips it too; an error on root itself is returned.
func FindFile(ctx context.Context, root string, match func(name string) bool) (string, bool, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path != root && errors.Is(walkErr, fs.ErrPermission) {
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && match(entry.Name()) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return found, found != "", nil
}

// ProbeTranscript reports a native session's availability from its transcript:
// unknown without a config dir or when the lookup fails, otherwise available
// exactly while locate finds the file.
func ProbeTranscript(ctx context.Context, ref ports.NativeSessionRef, locate func(context.Context, ports.NativeSessionRef) (string, bool, error)) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, ok, err := locate(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
