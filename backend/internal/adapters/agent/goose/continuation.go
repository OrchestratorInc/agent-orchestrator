package goose

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite" // provider history is read-only

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value := func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	if root := value("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return filepath.Join(root, "data"), nil
	}
	if root := value("XDG_DATA_HOME"); root != "" {
		return filepath.Join(root, "goose"), nil
	}
	if runtime.GOOS == "windows" {
		if appData := value("APPDATA"); appData != "" {
			return filepath.Join(appData, "Block", "goose", "data"), nil
		}
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "goose"), err
}

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, found, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if found {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// A Goose transcript is a row identity in a global SQLite store, as for
// OpenCode. Returning the native id lets AO locate it again after later turns.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(ref.ConfigDir) == "" || strings.TrimSpace(ref.NativeSessionID) == "" {
		return "", false, nil
	}
	path := filepath.Join(ref.ConfigDir, "sessions", "sessions.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", false, err
	}
	defer func() { _ = db.Close() }()
	var id string
	err = db.QueryRowContext(ctx, "SELECT id FROM sessions WHERE id = ? AND archived_at IS NULL", ref.NativeSessionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}
