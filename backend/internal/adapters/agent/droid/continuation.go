package droid

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// factoryHomeEnv overrides Droid's home directory; Droid then appends
// ".factory" to it. Unlike CLAUDE_CONFIG_DIR / CODEX_HOME, it is a home
// override rather than the state directory itself.
const (
	factoryHomeEnv = "FACTORY_HOME_OVERRIDE"
	factoryDirName = ".factory"
)

var (
	_ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionConfigProvider    = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber            = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator              = (*Plugin)(nil)
)

// ContinuationCapabilities reports that Droid accepts an appended system prompt
// on fresh launch and on resume, and that Droid assigns each conversation's
// native id itself (AO captures it through the session hooks GetRestoreCommand
// resumes from with `droid -r <id>`).
func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned,
	}
}

// NativeSessionConfigDir returns Droid's state root: <FACTORY_HOME_OVERRIDE or
// HOME>/.factory. The home override is read from the launch env first so it
// describes the child process, then the daemon environment.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home, err := factoryHome(env)
	if err != nil {
		return "", fmt.Errorf("droid: resolve config dir: %w", err)
	}
	return filepath.Join(home, factoryDirName), nil
}

// factoryHome resolves the home Droid appends ".factory" to. An override in env,
// including an explicitly empty one, takes precedence over the daemon
// environment because env describes the child process being launched.
func factoryHome(env map[string]string) (string, error) {
	if value, ok := env[factoryHomeEnv]; ok {
		if home := strings.TrimSpace(value); home != "" {
			return home, nil
		}
		return envHome(env)
	}
	if home := strings.TrimSpace(os.Getenv(factoryHomeEnv)); home != "" {
		return home, nil
	}
	return envHome(env)
}

func envHome(env map[string]string) (string, error) {
	if home := strings.TrimSpace(env["HOME"]); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

// ProbeNativeSession treats Droid's local JSONL transcript as the backing state
// for `droid -r`: the conversation is resumable while its <id>.jsonl exists.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, ok, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// LocateTranscript finds Droid's provider-owned <id>.jsonl under
// <config>/sessions. Droid resolves a session by id across every cwd-dashed
// subfolder (and the flat and "btw" locations), so this mirrors that lookup
// rather than depending on the lossy cwd-to-folder encoding.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID, err := canonicalDroidNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	sessionsDir := filepath.Join(configDir, "sessions")
	file := sessionID + ".jsonl"

	// Flat and "btw" locations first, matching Droid's own resolver order.
	for _, candidate := range []string{filepath.Join(sessionsDir, file), filepath.Join(sessionsDir, "btw", file)} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true, nil
		}
	}

	folders, err := os.ReadDir(sessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("droid: read transcript sessions: %w", err)
	}
	for _, folder := range folders {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !folder.IsDir() || !strings.HasPrefix(folder.Name(), "-") {
			continue
		}
		path := filepath.Join(sessionsDir, folder.Name(), file)
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			return path, true, nil
		case errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrPermission):
			// A missing or unreadable session folder is simply not a match.
			continue
		case err != nil:
			return "", false, fmt.Errorf("droid: stat transcript: %w", err)
		}
	}
	return "", false, nil
}

func canonicalDroidNativeSessionID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("droid: invalid native session id: %w", err)
	}
	return parsed.String(), nil
}
