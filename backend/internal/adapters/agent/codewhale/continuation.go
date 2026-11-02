package codewhale

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codewhalestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionPreparer     = (*Plugin)(nil)
)

// NativeSessionConfigDir selects the native profile, including legacy sessions.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := nativeconfig.Resolve(env, "CODEWHALE_HOME", ".codewhale")
	if err != nil {
		return "", err
	}
	_, explicit := env["CODEWHALE_HOME"]
	if explicit || os.Getenv("CODEWHALE_HOME") != "" {
		return root, nil
	}
	if info, err := os.Stat(filepath.Join(root, "sessions")); err == nil && info.IsDir() {
		return root, nil
	}
	legacy := filepath.Join(filepath.Dir(root), ".deepseek")
	if info, err := os.Stat(filepath.Join(legacy, "sessions")); err == nil && info.IsDir() {
		return legacy, nil
	}
	return root, nil
}

// LocateTranscript validates the exact native snapshot; it never picks a prefix.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if ref.ConfigDir == "" || ref.NativeSessionID == "" {
		return "", false, nil
	}
	if !nativeSessionIDPattern.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("invalid codewhale native session id")
	}
	path := filepath.Join(ref.ConfigDir, "sessions", ref.NativeSessionID+".json")
	archive, err := codewhalestore.Read(ctx, ref.ConfigDir, path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, archive.ID == ref.NativeSessionID, nil
}

// ProbeNativeSession checks the resumable native document without invoking CLI.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, found, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if found {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// PrepareImportedSession copies native history under a worktree-specific id,
// removing the old Runtime binding so Codewhale creates authority in that root.
func (*Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	archive, err := codewhalestore.Read(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if archive.ID != r.NativeSessionID {
		return ports.NativeSessionRef{}, errors.New("codewhale source no longer holds the imported conversation")
	}
	copy, path, err := codewhalestore.Copy(ctx, r.ConfigDir, r.TranscriptPath, r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: copy.ID, ConfigDir: r.ConfigDir, TranscriptPath: path}, nil
}
