package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
)

var openCodeSessionID = regexp.MustCompile(`^ses_[A-Za-z0-9]+$`)

func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	lookup := func(key string) string {
		if env != nil {
			if value, ok := env[key]; ok {
				return strings.TrimSpace(value)
			}
		}
		return strings.TrimSpace(os.Getenv(key))
	}
	if dir := lookup("OPENCODE_DATA_DIR"); dir != "" {
		return filepath.Clean(dir), nil
	}
	if dataHome := lookup("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "opencode"), nil
	}
	home := lookup("HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("opencode: resolve data dir: %w", err)
		}
	}
	return filepath.Join(home, ".local", "share", "opencode"), nil
}

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	configDir := strings.TrimSpace(ref.ConfigDir)
	sessionID := strings.TrimSpace(ref.NativeSessionID)
	if configDir == "" || sessionID == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	if !openCodeSessionID.MatchString(sessionID) {
		return ports.NativeSessionAvailabilityUnavailable, nil
	}
	dbPath := filepath.Join(configDir, "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.NativeSessionAvailabilityUnavailable, nil
		}
		return ports.NativeSessionAvailabilityUnknown, err
	}
	db, err := sql.Open("sqlite", ReadOnlyDSN(dbPath, 2000))
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	defer func() { _ = db.Close() }()
	var count int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session
		WHERE id = ? AND time_archived IS NULL AND (parent_id IS NULL OR parent_id = '')`, sessionID).Scan(&count)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if count > 0 {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
