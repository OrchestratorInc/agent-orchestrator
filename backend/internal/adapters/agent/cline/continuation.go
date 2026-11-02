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
	_ ports.AgentTranscriptLocator          = (*Plugin)(nil)
)

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
	if root := value("CLINE_DATA_DIR"); len(root) > 0 {
		return filepath.Abs(root)
	}
	if root := value("CLINE_DIR"); len(root) > 0 {
		return filepath.Abs(filepath.Join(root, "data"))
	}
	home := env["HOME"]
	if len(home) == 0 {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".cline", "data"), nil
}

func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !clineNativeID.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("cline invalid native session id")
	}
	if len(ref.ConfigDir) == 0 {
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

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if len(ref.ConfigDir) == 0 {
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
