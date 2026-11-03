package vibe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

var vibeNativeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// NativeSessionConfigDir names the home containing the global session log.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return nativeconfig.Resolve(env, "VIBE_HOME", ".vibe")
}

// ProbeNativeSession treats only a readable native transcript as available.
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
	// Custom save_dir and the newer unified store may hold this ID.
	return ports.NativeSessionAvailabilityUnknown, nil
}

// LocateTranscript matches the full ID in metadata, never the lossy short suffix.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !vibeNativeID.MatchString(ref.NativeSessionID) || strings.TrimSpace(ref.ConfigDir) == "" {
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
	logs, err := root.Open(filepath.Join("logs", "session"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	entries, err := logs.ReadDir(-1)
	_ = logs.Close()
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session_") {
			continue
		}
		dir := filepath.Join("logs", "session", entry.Name())
		for _, name := range []string{"metadata.json", "meta.json"} {
			file, err := root.Open(filepath.Join(dir, name))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", false, err
			}
			var meta struct {
				ID string `json:"session_id"`
			}
			err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&meta)
			_ = file.Close()
			if err == nil && meta.ID == ref.NativeSessionID {
				path := filepath.Join(dir, "messages.jsonl")
				info, err := root.Stat(path)
				if err != nil {
					return "", false, err
				}
				if info.Mode().IsRegular() {
					return filepath.Join(ref.ConfigDir, path), true, nil
				}
			}
			break
		}
	}
	return "", false, nil
}
