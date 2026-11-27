package pax

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only native session probes

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// configDirName is Pax's folder under $XDG_CONFIG_HOME (default ~/.config,
	// on every platform). legacyConfigDirName is the folder before the package
	// became pydantic-ai-pax; Pax keeps using it when it could not move it.
	configDirName       = "pydantic-ai-pax"
	legacyConfigDirName = "pydantic-clai2"
	sessionsDatabase    = "sessions.db"
)

// configDir is the Pax folder the launched process reads, from the launch
// environment's XDG_CONFIG_HOME when it sets one.
func configDir(env map[string]string) (string, error) {
	parent, err := nativeconfig.Resolve(env, "XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", fmt.Errorf("%s: resolve config dir: %w", adapterID, err)
	}
	current := filepath.Join(parent, configDirName)
	if _, err := os.Stat(current); err == nil {
		return current, nil
	}
	legacy := filepath.Join(parent, legacyConfigDirName)
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy, nil
	}
	return current, nil
}

// savedConversation is what Pax's store holds for one conversation id.
type savedConversation struct {
	found     bool
	workspace string
}

// NativeConversationExists reports whether Pax saved the conversation. Pax
// saves one only after its first turn, so an id AO assigned at launch can
// still be missing.
func (p *Plugin) NativeConversationExists(ctx context.Context, _ ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	saved, err := p.savedConversation(ctx, nativeID, env)
	return saved.found, err
}

// savedConversation reads the conversation's row from sessions.db without
// writing or checkpointing it. A missing database holds no conversations.
func (*Plugin) savedConversation(ctx context.Context, nativeID string, env map[string]string) (savedConversation, error) {
	if err := ctx.Err(); err != nil {
		return savedConversation{}, err
	}
	dir, err := configDir(env)
	if err != nil {
		return savedConversation{}, err
	}
	path := filepath.Join(dir, sessionsDatabase)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return savedConversation{}, nil
		}
		return savedConversation{}, fmt.Errorf("%s: stat sessions database: %w", adapterID, err)
	}
	db, err := sql.Open("sqlite", nativeconfig.KiloReadOnlyDatabaseURI(path))
	if err != nil {
		return savedConversation{}, fmt.Errorf("%s: open sessions database: %w", adapterID, err)
	}
	defer func() { _ = db.Close() }()
	var workspace sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT json_extract(metadata, '$.workspace') FROM conversations WHERE id = ?`, nativeID).Scan(&workspace)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return savedConversation{}, nil
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return savedConversation{}, nil
	case err != nil:
		return savedConversation{}, fmt.Errorf("%s: read sessions database: %w", adapterID, err)
	}
	return savedConversation{found: true, workspace: workspace.String}, nil
}

// sameWorkspace compares the folder Pax saved (its resolved launch directory)
// with the session's worktree.
func sameWorkspace(saved, workspace string) bool {
	if strings.TrimSpace(saved) == "" || strings.TrimSpace(workspace) == "" {
		return false
	}
	a, b := canonicalPath(saved), canonicalPath(workspace)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}

// launchRecord is AO's note of a session's latest Pax launch, kept under the
// AO data dir.
type launchRecord struct {
	NativeID   string    `json:"nativeId"`
	Workspace  string    `json:"workspace"`
	LaunchedAt time.Time `json:"launchedAt"`
}

func launchRecordPath(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "agents", adapterID, sessionID, "launch.json")
}

func readLaunchRecord(dataDir, sessionID string) (launchRecord, bool, error) {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(sessionID) == "" {
		return launchRecord{}, false, nil
	}
	data, err := os.ReadFile(launchRecordPath(dataDir, sessionID)) //nolint:gosec // AO-owned path under the data dir
	if errors.Is(err, os.ErrNotExist) {
		return launchRecord{}, false, nil
	}
	if err != nil {
		return launchRecord{}, false, err
	}
	var record launchRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return launchRecord{}, false, fmt.Errorf("%s: read launch record: %w", adapterID, err)
	}
	return record, true, nil
}

func (p *Plugin) saveLaunchRecord(dataDir, sessionID string, record launchRecord) error {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	path := launchRecordPath(dataDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("%s: create session dir: %w", adapterID, err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, data, 0o600)
}
