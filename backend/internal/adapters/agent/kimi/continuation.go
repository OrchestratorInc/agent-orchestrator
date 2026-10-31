package kimi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// kimiNativeSessionID matches a Kimi session id: `session_<uuid>`, the directory
// name `kimi --session` and ACP session/load both accept.
var kimiNativeSessionID = regexp.MustCompile(`^session_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

// NativeSessionConfigDir returns the Kimi Code state root used by a launch:
// KIMI_CODE_HOME when set, otherwise ~/.kimi-code. AO stores this with the
// native-session binding so a later probe reads the same home the session was
// created in rather than AO's isolated managed home.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, kimiCodeHomeEnv, ".kimi-code")
	if err != nil {
		return "", fmt.Errorf("kimi: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession reports whether the imported conversation still has its
// local transcript, the authoritative backing state for a resume.
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

// LocateTranscript finds the main agent's wire.jsonl for a Kimi session without
// depending on the lossy workdir folder name: the session_<uuid> directory is
// globally unique, so scanning the immediate session folders is deterministic.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID := strings.TrimSpace(ref.NativeSessionID)
	if !kimiNativeSessionID.MatchString(sessionID) {
		return "", false, fmt.Errorf("kimi: invalid native session id")
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	sessionsDir := filepath.Join(configDir, "sessions")
	workdirs, err := os.ReadDir(sessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("kimi: read sessions: %w", err)
	}
	for _, workdir := range workdirs {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !workdir.IsDir() {
			continue
		}
		path := filepath.Join(sessionsDir, workdir.Name(), sessionID, "agents", "main", "wire.jsonl")
		info, err := os.Stat(path)
		switch {
		case err == nil:
			if info.Mode().IsRegular() {
				return path, true, nil
			}
		case errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrPermission):
			// A missing or unreadable session folder is simply not a match.
		default:
			return "", false, fmt.Errorf("kimi: stat transcript: %w", err)
		}
	}
	return "", false, nil
}
