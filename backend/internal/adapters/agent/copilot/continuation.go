package copilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

// NativeSessionConfigDir follows Copilot's COPILOT_HOME override and default
// ~/.copilot. A launch-specific value takes precedence over the daemon's.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return nativeconfig.Resolve(env, "COPILOT_HOME", ".copilot")
}

// ProbeNativeSession checks the store session/load reads. Copilot addresses
// CLI and ACP conversations by the same UUID regardless of the launch cwd;
// imports do not relocate or copy their native event logs.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if err := ctx.Err(); err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
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

// LocateTranscript validates the UUID and confines the native lookup to the
// selected provider home, including intermediate directory symlinks.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	id, err := uuid.Parse(strings.TrimSpace(ref.NativeSessionID))
	if err != nil {
		return "", false, fmt.Errorf("copilot: invalid native session id: %w", err)
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	root, err := os.OpenRoot(configDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	rel := filepath.Join("session-state", id.String(), copilotTranscriptName)
	info, err := root.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", false, nil
	}
	return filepath.Join(configDir, rel), true, nil
}
