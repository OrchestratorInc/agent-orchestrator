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

// OpenCode continuation for imported chats. Unlike the file-per-conversation
// providers, OpenCode records every session as a row in one SQLite database
// (opencode.db), so the native state root is the data directory and session
// existence is a read-only row lookup, never a transcript file on disk.

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
)

// openCodeSessionID matches OpenCode's session identifiers (for example
// ses_e830c4a8affelmmC9j6HFbCZAr).
var openCodeSessionID = regexp.MustCompile(`^ses_[A-Za-z0-9]+$`)

// NativeSessionConfigDir resolves OpenCode's data directory exactly as the
// provider does: OPENCODE_DATA_DIR, else $XDG_DATA_HOME/opencode, else
// ~/.local/share/opencode. The launch env takes precedence over the process
// environment so a project-scoped override is honored.
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

// ProbeNativeSession reports whether the imported conversation still backs a
// resumable OpenCode session: a non-archived, top-level row in opencode.db.
// A missing database or row is unavailable; an unreadable one is unknown so a
// transient lock does not look like deletion.
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
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_pragma=busy_timeout(2000)")
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
