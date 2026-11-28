package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A restore runs the project's Setup only when it finds the session's
// worktree missing and recreates it. The worktree is created before Setup
// runs, so a Setup that fails or is cut off (a client that hangs up cancels
// the restore and kills its Setup step) leaves a worktree that exists but was
// never provisioned. The next restore would find the path present and skip
// Setup, launching the agent without the project's dependencies.
//
// The debt is therefore recorded outside the worktree as soon as a restore
// knows it will recreate the worktree, before anything that can fail after
// the worktree exists (attachments, inventory rows, Setup itself), and it is
// cleared only when Setup finishes. Every later restore runs Setup again until
// one run completes.

// setupOwedPath is the marker for a session whose recreated worktree still
// owes Setup, or "" when the manager has no data directory.
func (m *Manager) setupOwedPath(id domain.SessionID) string {
	name := string(id)
	if m.dataDir == "" || name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return ""
	}
	return filepath.Join(m.dataDir, "setup-owed", name)
}

// setupOwed reports whether a session's worktree still owes Setup: it was
// recreated now, or an earlier recreation's Setup never finished.
func (m *Manager) setupOwed(id domain.SessionID, recreated bool) bool {
	if recreated {
		return true
	}
	path := m.setupOwedPath(id)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// noteSetupOwed records that the session's worktree owes Setup. Call it before
// recreating the worktree, so any failure from then on leaves the debt behind.
func (m *Manager) noteSetupOwed(id domain.SessionID) {
	path := m.setupOwedPath(id)
	if path == "" {
		return
	}
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err == nil {
		err = os.WriteFile(path, nil, 0o600)
	}
	if err != nil && m.logger != nil {
		m.logger.Warn("record owed setup", "sessionID", id, "error", err)
	}
}

// runOwedSetup runs provision when the worktree owes Setup and clears the debt
// only once provision succeeds.
func (m *Manager) runOwedSetup(id domain.SessionID, recreated bool, provision func() error) error {
	if !m.setupOwed(id, recreated) {
		return nil
	}
	if err := provision(); err != nil {
		return err
	}
	if path := m.setupOwedPath(id); path != "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && m.logger != nil {
			m.logger.Warn("clear owed setup", "sessionID", id, "error", err)
		}
	}
	return nil
}
