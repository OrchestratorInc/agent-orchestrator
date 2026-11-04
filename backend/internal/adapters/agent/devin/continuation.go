package devin

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NativeSessionConfigDir resolves the data root used by the native CLI.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	lookup := func(key string) string {
		if value, ok := env[key]; ok {
			return value
		}
		return os.Getenv(key)
	}
	if root := lookup("XDG_DATA_HOME"); root != "" {
		return filepath.Join(root, "devin", "cli"), nil
	}
	home := lookup("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".local", "share", "devin", "cli"), nil
}

// ProbeNativeSession checks the native SQLite row. A row is not suitable for
// LocateTranscript. Unknown query failures must never imply deletion.
func (*Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	path := filepath.Join(ref.ConfigDir, "sessions.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.NativeSessionAvailabilityUnavailable, nil
		}
		return ports.NativeSessionAvailabilityUnknown, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id=? AND hidden=0", ref.NativeSessionID).Scan(&count); err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if count > 0 {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
