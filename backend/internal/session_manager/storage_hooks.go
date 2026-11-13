package sessionmanager

import (
	"context"
	"errors"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrSessionNotQuiet refuses a storage offload while the session's agent may
// still be running: offloading under a live agent would pull its dependencies
// out from under it.
var ErrSessionNotQuiet = errors.New("session: agent may still be running")

// StorageHooks lets the storage service restore and seed a session's
// dependencies around the manager's own workspace work. Without hooks the
// manager behaves exactly as before.
type StorageHooks interface {
	// RestoreDependencies brings back an offloaded session's dependencies before
	// its agent runs again. A nil finish means the session needs nothing. When
	// needsSetup is true the manager runs the project's Setup and passes its
	// outcome to finish; otherwise finish(nil) records a restore the hook did
	// itself (a copy-on-write clone).
	RestoreDependencies(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) (needsSetup bool, finish func(setupErr error), err error)
	// SeedWorkspace clones cached dependencies into a worktree before Setup
	// runs. It is best effort and never fails the start.
	SeedWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string)
}

type storageHooksSlot struct {
	mu    sync.RWMutex
	hooks StorageHooks
}

// SetStorageHooks installs the storage service's hooks. Call it once while the
// daemon is wiring services; nil removes them.
func (m *Manager) SetStorageHooks(hooks StorageHooks) {
	m.storage.mu.Lock()
	defer m.storage.mu.Unlock()
	m.storage.hooks = hooks
}

func (m *Manager) storageHooks() StorageHooks {
	m.storage.mu.RLock()
	defer m.storage.mu.RUnlock()
	return m.storage.hooks
}

// WithQuietSession runs fn while the session holds an exclusive storage
// operation and its project's workspace gate, after re-reading the record and
// confirming the session is open and its agent is not running. Holding the
// operation closes input admission, so no message, resume, kill or provider
// switch can start until fn returns. fn receives the current record.
func (m *Manager) WithQuietSession(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord) error) error {
	if err := m.beginAgentOperation(ctx, id, agentOperationStorageOffload); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return ErrOperationInProgress
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationStorageOffload)
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if rec.IsTerminated {
		return ErrTerminated
	}
	if !m.sessionAgentQuiet(ctx, rec) {
		return ErrSessionNotQuiet
	}
	release := m.acquireWorkspaceGate(rec.ProjectID)
	defer release()
	return fn(rec)
}

// sessionAgentQuiet reports a session whose agent is provably not running: a
// Chat session that hibernated or exited with no live controller, or a
// terminal session whose agent exited and whose launched process is gone.
// Anything uncertain reads as not quiet.
func (m *Manager) sessionAgentQuiet(ctx context.Context, rec domain.SessionRecord) bool {
	if rec.IsTaskPreparation || rec.ProvisionState.IsProvisioning() {
		return false
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat != nil && m.chat.HasLiveChatController(rec.ID) {
			return false
		}
		return rec.HibernatedAt != nil || rec.Activity.State == domain.ActivityExited
	}
	if rec.Activity.State != domain.ActivityExited {
		return false
	}
	return !m.launchedTUIAgentStillAlive(ctx, rec)
}

// restoreOffloadedDependencies restores an offloaded session's dependencies
// before its agent runs again. A failure is logged and recorded but never
// blocks the resume: the session stays usable and the next resume retries.
func (m *Manager) restoreOffloadedDependencies(ctx context.Context, rec domain.SessionRecord) {
	hooks := m.storageHooks()
	if hooks == nil || rec.Metadata.WorkspacePath == "" {
		return
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		m.logger.Warn("restore offloaded dependencies: load project", "sessionID", rec.ID, "error", err)
		return
	}
	needsSetup, finish, err := hooks.RestoreDependencies(ctx, project, rec)
	if err != nil {
		m.logger.Warn("restore offloaded dependencies", "sessionID", rec.ID, "error", err)
		return
	}
	if finish == nil {
		return
	}
	var setupErr error
	if needsSetup {
		setupCtx, cancel := ports.ExtendSpawnRequestBudget(ctx, postCreateBudget(0, project.Config.PostCreate))
		setupErr = m.provisionWorkspace(setupCtx, project, rec.Metadata.WorkspacePath)
		cancel()
		if setupErr != nil {
			m.logger.Warn("restore offloaded dependencies: setup failed", "sessionID", rec.ID, "error", setupErr)
		}
	}
	finish(setupErr)
}

// seedWorkspaceDependencies lets the storage service clone cached dependencies
// into a worktree before its Setup runs.
func (m *Manager) seedWorkspaceDependencies(ctx context.Context, project domain.ProjectRecord, workspacePath string) {
	if hooks := m.storageHooks(); hooks != nil {
		hooks.SeedWorkspace(ctx, project, workspacePath)
	}
}
