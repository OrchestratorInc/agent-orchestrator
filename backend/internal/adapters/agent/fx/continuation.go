package fx

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

var fxNativeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var (
	_ ports.AgentNativeSessionProber = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator   = (*Plugin)(nil)
)

// LocateTranscript prefers the v2 journal after conversion. All paths remain
// inside the native home and symlinked transcript leaves are not accepted.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !fxNativeID.MatchString(ref.NativeSessionID) {
		return "", false, errors.New("fx: invalid native session id")
	}
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return "", false, nil
	}
	r, err := os.OpenRoot(ref.ConfigDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer func() { _ = r.Close() }()
	for _, rel := range []string{
		filepath.Join("sessions", "v2", ref.NativeSessionID, "log.jsonl"),
		filepath.Join("sessions", ref.NativeSessionID, "events.jsonl"),
	} {
		info, err := r.Lstat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		f, err := r.Open(rel)
		if err != nil {
			return "", false, err
		}
		_ = f.Close()
		return filepath.Join(ref.ConfigDir, rel), true, nil
	}
	return "", false, nil
}

// ProbeNativeSession uses a local transcript as backing-state evidence;
// unreadable homes remain unknown.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, ctx.Err()
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
