package cline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var clineNativeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

// NativeSessionConfigDir resolves Cline's data root for the launch environment.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value := func(key string) string {
		if v, set := env[key]; set {
			return v
		}
		return os.Getenv(key)
	}
	if root := value("CLINE_DATA_DIR"); root != "" {
		return filepath.Abs(root)
	}
	if root := value("CLINE_DIR"); root != "" {
		return filepath.Abs(filepath.Join(root, "data"))
	}
	home := env["HOME"]
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".cline", "data"), nil
}

// LocateTranscript finds Cline's native message document within its data root.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !clineNativeID.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("cline invalid native session id")
	}
	if ref.ConfigDir == "" {
		return "", false, nil
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	rel := filepath.Join("sessions", ref.NativeSessionID, ref.NativeSessionID+".messages.json")
	info, err := root.Stat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, rel), info.Mode().IsRegular() && info.Size() > 0, nil
}

// ProbeNativeSession reports whether Cline's local message document is available.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, ctx.Err()
	}
	_, exists, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if exists {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
