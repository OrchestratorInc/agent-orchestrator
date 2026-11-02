package hermes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NativeSessionConfigDir resolves the state home used for native resume.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ResolveHome(env)
}

// ProbeNativeSession checks for the native conversation without writing state.
func (*Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if _, err := os.Stat(filepath.Join(ref.ConfigDir, "state.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.NativeSessionAvailabilityUnavailable, nil
		}
		return ports.NativeSessionAvailabilityUnknown, err
	}
	db, err := sql.Open("sqlite", nativeStateDSN(ref.ConfigDir))
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id=?", ref.NativeSessionID).Scan(&count); err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if count > 0 {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// Imports have a native id but no AO-created title file. Never fall back after
// a corrupt/unreadable title file: that would hide damage to a managed session.
func (p *Plugin) importedRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if id == "" || cfg.Session.Metadata["importedNativeSession"] != "true" {
		return nil, false, nil
	}
	if _, err := os.Stat(filepath.Join(sessionDir(cfg.DataDir, cfg.Session.ID), "native-title")); !errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	launch := ports.LaunchConfig{DataDir: cfg.DataDir, SessionID: cfg.Session.ID, WorkspacePath: cfg.Session.WorkspacePath, Config: cfg.Config, Permissions: cfg.Permissions, AllowedTools: cfg.AllowedTools, DisallowedTools: cfg.DisallowedTools}
	if err := validate(launch); err != nil {
		return nil, false, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd := []string{binary, "chat", "--cli", "--resume", id, "--no-restore-cwd", "--in", cfg.Session.WorkspacePath}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	agentbase.AppendEffortFlag(&cmd, cfg.Config, "--reasoning")
	return append(cmd, permissionArgs(cfg.Permissions)...), true, nil
}
