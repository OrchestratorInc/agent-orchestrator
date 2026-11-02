package autohand

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var autohandNativeID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}-[0-9]+$`)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

// NativeSessionConfigDir mirrors AUTOHAND_HOME, not AUTOHAND_CONFIG (which
// selects only a config file). A launch-local empty override uses the default.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, set := env["AUTOHAND_HOME"]
	if !set {
		root = os.Getenv("AUTOHAND_HOME")
	}
	if root = strings.TrimSpace(root); root != "" {
		return filepath.Abs(root)
	}
	home := env["HOME"]
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".autohand"), nil
}

// LocateTranscript finds Autohand's native conversation within its state root.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !autohandNativeID.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("autohand invalid native session id")
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
	rel := filepath.Join("sessions", ref.NativeSessionID, "conversation.jsonl")
	info, err := root.Stat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, rel), info.Mode().IsRegular() && info.Size() > 0, nil
}

// ProbeNativeSession reports whether Autohand's local conversation is available.
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
