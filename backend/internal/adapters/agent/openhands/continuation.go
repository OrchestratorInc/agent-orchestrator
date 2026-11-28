package openhands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)

// NativeSessionConfigDir returns the global conversation store, not the settings home.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	lookup := func(key string) string {
		if value, ok := env[key]; ok {
			return value
		}
		return os.Getenv(key)
	}
	if dir := lookup("OPENHANDS_CONVERSATIONS_DIR"); dir != "" {
		return dir, nil
	}
	if dir := lookup("OPENHANDS_PERSISTENCE_DIR"); dir != "" {
		return filepath.Join(dir, "conversations"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".openhands", "conversations"), nil
}

// ProbeNativeSession verifies the persisted state belongs to the requested ID.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
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

// LocateTranscript returns base_state.json; it identifies the adjacent events store.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	id, err := uuid.Parse(ref.NativeSessionID)
	if err != nil || ref.ConfigDir == "" {
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
	// The CLI/SDK names folders with UUID.hex, without dashes.
	path := filepath.Join(strings.ReplaceAll(id.String(), "-", ""), "base_state.json")
	file, err := root.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = file.Close() }()
	var state struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(&state); err != nil {
		return "", false, err
	}
	stored, err := uuid.Parse(state.ID)
	if err != nil || stored != id {
		return "", false, errors.New("openhands native identity mismatch")
	}
	return filepath.Join(ref.ConfigDir, path), true, nil
}
