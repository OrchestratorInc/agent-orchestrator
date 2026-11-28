package sessionmanager

import (
	"errors"
	"fmt"
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
// recreated now, or an earlier recreation's Setup never finished. A marker
// that cannot be inspected is an error, never "nothing owed": guessing wrong
// would launch the agent in an unprovisioned worktree.
func (m *Manager) setupOwed(id domain.SessionID, recreated bool) (bool, error) {
	if recreated {
		return true, nil
	}
	path := m.setupOwedPath(id)
	if path == "" {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect owed setup: %w", err)
	}
	return true, nil
}

// noteSetupOwed records that the session's worktree owes Setup. Call it before
// recreating the worktree, and do not recreate it when this fails: an
// unrecorded recreation whose Setup then fails would be skipped from then on.
func (m *Manager) noteSetupOwed(id domain.SessionID) error {
	path := m.setupOwedPath(id)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("record owed setup: %w", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return fmt.Errorf("record owed setup: %w", err)
	}
	return nil
}

// runOwedSetup runs provision when the worktree owes Setup and clears the debt
// only once provision succeeds. A marker left behind after a successful Setup
// costs one extra Setup run, so failing to clear it is only logged.
func (m *Manager) runOwedSetup(id domain.SessionID, recreated bool, provision func() error) error {
	owed, err := m.setupOwed(id, recreated)
	if err != nil {
		return err
	}
	if !owed {
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
