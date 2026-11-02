package crush

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // read-only native session probes

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)
var _ ports.AgentImportedSessionRelocator = (*Plugin)(nil)

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
	if root := lookup("CRUSH_GLOBAL_DATA"); len(root) > 0 {
		return root, nil
	}
	if root := lookup("XDG_DATA_HOME"); len(root) > 0 {
		return filepath.Join(root, "crush"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "crush"), nil
}

func crushNativeDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return sql.Open("sqlite", uri.String()+"?mode=ro&_pragma=busy_timeout(2000)")
}

func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	id, err := uuid.Parse(ref.NativeSessionID)
	if err != nil || len(ref.ConfigDir) == 0 {
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
	file, err := root.Open("projects.json")
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = file.Close() }()
	var registry struct {
		Projects []struct {
			DataDir string `json:"data_dir"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 4<<20)).Decode(&registry); err != nil {
		return "", false, err
	}
	var lastErr error
	for _, project := range registry.Projects {
		if !filepath.IsAbs(project.DataDir) {
			continue
		}
		path := filepath.Join(project.DataDir, "crush.db")
		db, err := crushNativeDB(path)
		if err != nil {
			lastErr = err
			continue
		}
		var count int
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, id.String()).Scan(&count)
		_ = db.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if count > 0 {
			return filepath.Join(path, id.String()), true, nil
		}
	}
	return "", false, lastErr
}

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if len(strings.TrimSpace(ref.ConfigDir)) == 0 {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, ok, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnknown, nil
}
