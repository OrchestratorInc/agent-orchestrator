package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ErrCleanupScript marks a script failure that preserves the worktree for retry.
var ErrCleanupScript = errors.New("workspace cleanup script failed")

type cleanupStepError struct {
	step  int
	cause error
}

func (e *cleanupStepError) Error() string {
	reason := "command could not run"
	var exit *exec.ExitError
	var timedOut *stepTimeoutError
	switch {
	case errors.As(e.cause, &timedOut):
		reason = "timed out after " + stepMinutes(timedOut.timeout)
	case errors.Is(e.cause, context.Canceled):
		reason = "cancelled"
	case errors.Is(e.cause, exec.ErrWaitDelay):
		reason = "output pipe stayed open"
	case errors.As(e.cause, &exit):
		reason = fmt.Sprintf("exit status %d", exit.ExitCode())
	}
	return fmt.Sprintf("cleanup step %d failed: %s", e.step, reason)
}

func (e *cleanupStepError) Unwrap() []error { return []error{ErrCleanupScript, e.cause} }

// stepTimeoutError is a Cleanup step stopped at its own time limit.
type stepTimeoutError struct {
	timeout time.Duration
	cause   error
}

func (e *stepTimeoutError) Error() string { return "timed out after " + stepMinutes(e.timeout) }
func (e *stepTimeoutError) Unwrap() error { return e.cause }

// runPreRemove is called only for permanent AO-owned workspace retirement,
// after the session's processes have stopped and before worktree removal.
func (m *Manager) runPreRemove(projectID domain.ProjectID, workspacePath string) error {
	// Cleanup belongs to the daemon, not the request or its teardown deadline.
	// It has no execution timer, but shutdown must still stop it.
	return m.runPreRemoveContext(m.backgroundContext, projectID, workspacePath)
}

// runReplacementPreRemove runs cleanup for a forced orchestrator retirement.
// Replacement holds the project's orchestrator lock and must release the
// branch, so a slow or failing script is logged instead of blocking or
// vetoing the forced removal.
func (m *Manager) runReplacementPreRemove(id domain.SessionID, projectID domain.ProjectID, workspacePath string) {
	ctx, cancel := context.WithTimeout(m.backgroundContext, replacementCleanupBudget)
	defer cancel()
	if err := m.runPreRemoveContext(ctx, projectID, workspacePath); err != nil {
		m.logger.Warn("retire replacement: workspace cleanup failed; removing workspace anyway", "sessionID", id, "error", err)
	}
}

// replacementCleanupBudget bounds cleanup commands during orchestrator replacement.
var replacementCleanupBudget = killTeardownBudget

// ReplacementCleanupBudget is how long retiring one of the project's
// orchestrators for a replacement may spend in its Cleanup steps.
func ReplacementCleanupBudget(project domain.ProjectRecord) time.Duration {
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return 0
	}
	for _, step := range project.Config.PreRemove {
		if command, _ := scriptStepTimeout(step, 0); strings.TrimSpace(command) != "" {
			return replacementCleanupBudget
		}
	}
	return 0
}

func (m *Manager) runPreRemoveContext(ctx context.Context, projectID domain.ProjectID, workspacePath string) error {
	if workspacePath == "" {
		return nil
	}
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return err
	}
	if project.Kind.WithDefault() == domain.ProjectKindScratch || len(project.Config.PreRemove) == 0 {
		return nil
	}
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect workspace for cleanup: %w", err)
	}
	managedRoot, err := filepath.EvalSymlinks(filepath.Join(m.dataDir, "worktrees"))
	if err != nil {
		return fmt.Errorf("resolve managed workspace root: %w", err)
	}
	physicalPath, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return fmt.Errorf("resolve workspace for cleanup: %w", err)
	}
	rel, err := filepath.Rel(managedRoot, physicalPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("cleanup path is outside managed workspaces")
	}
	for index, step := range project.Config.PreRemove {
		// A step runs without a limit unless it sets its own; its marker
		// line never reaches the shell (cmd on Windows has no # comments).
		command, timeout := scriptStepTimeout(step, 0)
		if strings.TrimSpace(command) == "" {
			continue
		}
		out := &cleanupOutput{}
		env := agentlaunch.MergeEnv(project.Config.Env, map[string]string{
			"AO_SOURCE_TREE_PATH": project.Path,
			"AO_WORKTREE_PATH":    workspacePath,
		})
		stepCtx, cancel := ctx, context.CancelFunc(func() {})
		if timeout > 0 {
			stepCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		err := runCleanupStep(stepCtx, workspacePath, command, env, out)
		// A step that exits cleanly right at its deadline succeeded; only a
		// failing step can have timed out.
		timedOut := err != nil && timeout > 0 && errors.Is(stepCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		if timedOut {
			err = &stepTimeoutError{timeout: timeout, cause: err}
		}
		if err != nil {
			m.logger.Warn("workspace cleanup step failed", "projectID", projectID, "step", index+1,
				"error", err, "output", agentlaunch.RedactValues(strings.TrimSpace(string(out.tail)), env))
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return &cleanupStepError{step: index + 1, cause: err}
		}
	}
	return nil
}

// runCleanupStep runs one Cleanup step; tests swap it to pin deadline races.
var runCleanupStep = runWorkspaceStep

// runWorkspaceStep shares shell, environment and pipe-drain handling for setup
// and cleanup. WaitDelay bounds orphaned output pipes after shell exit or
// cancellation; it does not impose a time limit on the running command.
func runWorkspaceStep(ctx context.Context, workspacePath, command string, env map[string]string, output io.Writer) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = aoprocess.CommandContext(ctx, "cmd", "/c", command)
	} else {
		cmd = aoprocess.CommandContext(ctx, "sh", "-c", command)
	}
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir = workspacePath
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	// A successful shell can leave a detached child holding its output pipes.
	// WaitDelay closes those pipes; it must not turn that shell success into a
	// script failure. Real exit errors and daemon cancellation still fail.
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		return nil
	}
	if exitedCleanlyAtCancel(ctx, cmd.ProcessState, err) {
		return nil
	}
	return err
}

// exitedCleanlyAtCancel reports a step whose shell exited 0 but whose context
// ended before Wait collected it. The tree kill then finds the group gone (or
// the shell a zombie) and reports no error, so Wait returns the context's
// error for a run that had finished.
func exitedCleanlyAtCancel(ctx context.Context, state *os.ProcessState, err error) bool {
	return err != nil && state != nil && state.Success() && ctx.Err() != nil && errors.Is(err, ctx.Err())
}

// cleanupOutput holds the last 4 KiB of output from a cleanup step.
type cleanupOutput struct{ tail []byte }

func (o *cleanupOutput) Write(p []byte) (int, error) {
	n := len(p)
	o.tail = append(o.tail, p...)
	if len(o.tail) > 4096 {
		o.tail = append([]byte(nil), o.tail[len(o.tail)-4096:]...)
	}
	return n, nil
}

// Cleanup facts already have CDC triggers, so completion also refreshes the UI.
// reportOnly applies to a generation's first record only: a later retry keeps
// whether an archive or a sweep started the cleanup.
func (m *Manager) recordWorkspaceCleanup(ctx context.Context, rec domain.SessionRecord, disposition domain.WorkspaceDisposition, code string, reportOnly bool) error {
	// Record cancellation too, while shutdown is draining background workers.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalIntentBudget)
	defer cancel()
	now := time.Now().UTC()
	facts, ok, err := m.store.GetSessionCleanupFacts(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !ok || facts.SessionGeneration != rec.CleanupGeneration {
		facts = domain.SessionCleanupRecord{SessionID: rec.ID, SessionGeneration: rec.CleanupGeneration, RuntimeReleasedAt: now, ReportOnly: reportOnly}
	}
	if disposition == domain.DispositionPending {
		facts.AttemptCount++
		facts.LastAttemptAt = now
	}
	facts.WorkspaceDisposition, facts.FailureCode = disposition, code
	facts.NextAttemptAt = time.Time{}
	return m.store.UpsertSessionCleanupFacts(ctx, facts)
}

func (m *Manager) recordWorkspaceCleanupResult(ctx context.Context, rec domain.SessionRecord, refusal cleanupRefusal, reportOnly bool) error {
	disposition, code := domain.DispositionRemoved, ""
	switch {
	case refusal.code.Retryable():
		// Still accepted, not failed: the background pass retries it.
		disposition = domain.DispositionPending
	case refusal.code == domain.CleanupSkipDirty:
		disposition, code = domain.DispositionPreservedDirty, "WORKSPACE_DIRTY"
	case refusal.refused():
		disposition, code = domain.DispositionFailed, "WORKSPACE_CLEANUP_FAILED"
	}
	return m.recordWorkspaceCleanup(ctx, rec, disposition, code, reportOnly)
}

// cleanupRefusal is why cleanup left a workspace alone this run. The zero
// value means the workspace was reclaimed.
type cleanupRefusal struct {
	code   domain.CleanupSkipCode
	reason string
}

func (r cleanupRefusal) refused() bool { return r.code != "" }

// Refusals that do not come from a teardown error; cleanupRefusalFor
// classifies those.
var (
	refusedDirty             = cleanupRefusal{domain.CleanupSkipDirty, "workspace has uncommitted changes"}
	refusedInUse             = cleanupRefusal{domain.CleanupSkipInUse, "workspace in use by a live session"}
	refusedShellTerminalOpen = cleanupRefusal{domain.CleanupSkipShellTerminalOpen, "shell terminal still open"}
	refusedAlreadyRunning    = cleanupRefusal{domain.CleanupSkipAlreadyRunning, "workspace cleanup is already running"}
	refusedAttachmentsFailed = cleanupRefusal{domain.CleanupSkipAttachmentsFailed, "attachment preservation failed"}
	refusedTeardownFailed    = cleanupRefusal{domain.CleanupSkipTeardownFailed, "workspace teardown failed"}
)

// recordSettledWorkspaceCleanup records a teardown outcome that needs no
// retry, so later cleanup runs and their dry-run preview skip the session. A
// failed write is only logged: the workspace is already settled, and the next
// cleanup simply revisits the session and finds nothing left.
func (m *Manager) recordSettledWorkspaceCleanup(ctx context.Context, rec domain.SessionRecord, disposition domain.WorkspaceDisposition) {
	if err := m.recordWorkspaceCleanup(ctx, rec, disposition, "", false); err != nil {
		m.logger.Warn("record workspace cleanup", "sessionID", rec.ID, "disposition", disposition, "error", err)
	}
}

// workspaceCleanupSettled reports a session whose current generation already
// released its workspace, or never had one.
func (m *Manager) workspaceCleanupSettled(ctx context.Context, rec domain.SessionRecord) bool {
	facts, ok, err := m.store.GetSessionCleanupFacts(ctx, rec.ID)
	if err != nil {
		m.logger.Warn("read workspace cleanup facts", "sessionID", rec.ID, "error", err)
		return false
	}
	return ok && releasedForGeneration(rec, facts)
}

// cleanupFactsBatchStore reads many sessions' cleanup facts in one query. It
// is optional so focused store fakes need not implement it.
type cleanupFactsBatchStore interface {
	ListSessionCleanupFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionCleanupRecord, error)
}

// settledWorkspaceCleanups is workspaceCleanupSettled for every terminated
// session in recs, read in one query where the store supports it.
func (m *Manager) settledWorkspaceCleanups(ctx context.Context, recs []domain.SessionRecord) map[domain.SessionID]bool {
	settled := map[domain.SessionID]bool{}
	batch, ok := m.store.(cleanupFactsBatchStore)
	if !ok {
		for _, rec := range recs {
			if rec.IsTerminated && m.workspaceCleanupSettled(ctx, rec) {
				settled[rec.ID] = true
			}
		}
		return settled
	}
	ids := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		if rec.IsTerminated {
			ids = append(ids, rec.ID)
		}
	}
	factsByID, err := batch.ListSessionCleanupFactsForSessions(ctx, ids)
	if err != nil {
		// Revisiting a settled session only finds nothing left to remove.
		m.logger.Warn("read workspace cleanup facts", "error", err)
		return settled
	}
	for _, rec := range recs {
		if facts, ok := factsByID[rec.ID]; ok && rec.IsTerminated && releasedForGeneration(rec, facts) {
			settled[rec.ID] = true
		}
	}
	return settled
}

func releasedForGeneration(rec domain.SessionRecord, facts domain.SessionCleanupRecord) bool {
	return facts.SessionGeneration == rec.CleanupGeneration && facts.WorkspaceDisposition.Settled()
}

// cleanupCandidateStore is the scan behind ReconcileWorkspaceCleanup. It is
// optional so focused store fakes need not implement it.
type cleanupCandidateStore interface {
	ListTerminalCleanupCandidates(ctx context.Context, now time.Time) ([]domain.SessionID, error)
}

// ReconcileWorkspaceCleanup is the daemon's periodic pass over terminated
// sessions whose workspace cleanup is unrecorded or unfinished. It finishes
// only what needs no decision from the user:
//
//   - a cleanup AO already accepted (kill or `ao session cleanup` recorded it
//     pending) that the daemon stopped before finishing, and
//   - sessions with nothing left on disk to remove, whose stale git
//     registration (if any) it prunes before recording the release.
//
// A worktree still on disk with no accepted cleanup is left for
// `ao session cleanup`: this pass never deletes one on AO's own initiative.
// Sessions holding restore markers or workspace-project rows are left alone.
func (m *Manager) ReconcileWorkspaceCleanup(ctx context.Context) error {
	lister, ok := m.store.(cleanupCandidateStore)
	if !ok {
		return nil
	}
	ids, err := lister.ListTerminalCleanupCandidates(ctx, m.clock())
	if err != nil {
		return fmt.Errorf("list cleanup candidates: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.reconcileWorkspaceCleanup(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("reconcile workspace cleanup %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) reconcileWorkspaceCleanup(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated {
		return err
	}
	facts, found, err := m.store.GetSessionCleanupFacts(ctx, id)
	if err != nil {
		return err
	}
	if found && facts.SessionGeneration == rec.CleanupGeneration && facts.WorkspaceDisposition == domain.DispositionPending {
		return m.resumeAcceptedCleanup(ctx, rec)
	}
	// Sessions this pass leaves alone are decided before taking the operation
	// guard, so revisiting them on every tick never blocks a restore.
	rows, err := m.store.ListSessionWorktrees(ctx, id)
	if err != nil || len(rows) > 0 {
		return err
	}
	if path := rec.Metadata.WorkspacePath; path != "" {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// The directory is gone, so teardown only prunes git's stale
		// registration. Cleanup's own path does that under the workspace
		// gate, skips a path a live successor (an orchestrator's) now holds,
		// and records the release.
		if _, refusal := m.cleanupWorkspaceUnderGate(ctx, rec, workspaceInfo(rec)); !refusal.refused() {
			m.cleanupSystemPromptDir(rec.ID)
		}
		return nil
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationKill); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return nil
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationKill)
	// Re-read under the operation: a restore that won the race has made the
	// session live.
	rec, ok, err = m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated || rec.Metadata.WorkspacePath != "" {
		return err
	}
	m.cleanupSystemPromptDir(rec.ID)
	return m.recordWorkspaceCleanup(ctx, rec, domain.DispositionNotApplicable, "", false)
}

// resumeAcceptedCleanup reruns a pending cleanup the same way a user retry
// does, then drops a workspace project's rows as a finished background kill
// cleanup would.
func (m *Manager) resumeAcceptedCleanup(ctx context.Context, rec domain.SessionRecord) error {
	rows, workspaceProject, err := m.workspaceProjectRows(ctx, rec)
	if err != nil {
		return err
	}
	if !workspaceProject && rec.Metadata.WorkspacePath == "" {
		return m.recordWorkspaceCleanup(ctx, rec, domain.DispositionNotApplicable, "", false)
	}
	_, refusal := m.cleanupWorkspaceUnderGate(ctx, rec, workspaceInfo(rec))
	if !refusal.refused() && workspaceProject && len(rows) > 0 {
		return m.store.DeleteSessionWorktrees(ctx, rec.ID)
	}
	return nil
}

var (
	// ErrSessionNotTerminated refuses deleting a session that is still live.
	ErrSessionNotTerminated = errors.New("session: not terminated")
	// ErrWorkspaceNotReleased refuses deleting a session whose workspace may
	// still be on disk: deleting the row would orphan the worktree, since
	// cleanup only reaches workspaces through their session.
	ErrWorkspaceNotReleased = errors.New("session: workspace not released")
)

// CleanupRefusedError is a delete whose cleanup-first step left the worktree
// in place, with the same code and reason `ao session cleanup` reports.
type CleanupRefusedError struct {
	Code   domain.CleanupSkipCode
	Reason string
}

func (e *CleanupRefusedError) Error() string { return "workspace cleanup refused: " + e.Reason }

// terminatedSessionDeleter is the store's session-record delete. It is
// optional so focused store fakes need not implement it.
type terminatedSessionDeleter interface {
	DeleteTerminatedSession(ctx context.Context, id domain.SessionID) (bool, error)
}

// DeleteSession permanently removes a terminated session's record once its
// workspace is released (or it never had one), along with its AO-owned files.
// With cleanup, a worktree still on disk is first cleaned up the way
// `ao session cleanup` does it, never forced. The delete holds the session's
// operation guard, so a restore cannot run between the checks and the delete.
// Branches and PRs are not touched, and the store keeps the session's spend in
// the agent account usage totals.
func (m *Manager) DeleteSession(ctx context.Context, id domain.SessionID, cleanup bool) error {
	deleter, ok := m.store.(terminatedSessionDeleter)
	if !ok {
		return fmt.Errorf("delete %s: session deletion is not supported by this store", id)
	}
	if cleanup {
		if err := m.cleanupBeforeDelete(ctx, id); err != nil {
			return fmt.Errorf("delete %s: %w", id, err)
		}
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationKill); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			err = ErrOperationInProgress
		}
		return fmt.Errorf("delete %s: %w", id, err)
	}
	defer m.endAgentOperation(id, agentOperationKill)
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("delete %s: %w", id, ErrNotFound)
	}
	if !rec.IsTerminated {
		return fmt.Errorf("delete %s: %w", id, ErrSessionNotTerminated)
	}
	if rec.Metadata.WorkspacePath != "" && !m.workspaceCleanupSettled(ctx, rec) {
		return fmt.Errorf("delete %s: %w", id, ErrWorkspaceNotReleased)
	}
	if rows, err := m.store.ListSessionWorktrees(ctx, id); err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	} else if len(rows) > 0 && !m.workspaceCleanupSettled(ctx, rec) {
		return fmt.Errorf("delete %s: %w", id, ErrWorkspaceNotReleased)
	}
	deleted, err := deleter.DeleteTerminatedSession(ctx, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if !deleted {
		return fmt.Errorf("delete %s: %w", id, ErrNotFound)
	}
	m.cleanupSystemPromptDir(id)
	m.cleanupAttachments(ctx, id)
	return nil
}

// cleanupBeforeDelete releases a terminated session's worktree the way Cleanup
// does. Anything else (a live or missing session) is left for the delete's own
// checks to refuse under the operation guard.
func (m *Manager) cleanupBeforeDelete(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated || rec.Metadata.WorkspacePath == "" || m.workspaceCleanupSettled(ctx, rec) {
		return err
	}
	if h := runtimeHandle(rec.Metadata); h.ID != "" {
		_ = m.runtime.Destroy(ctx, h) // best effort; usually already gone
	}
	if _, refusal := m.cleanupWorkspaceUnderGate(ctx, rec, workspaceInfo(rec)); refusal.refused() {
		return &CleanupRefusedError{Code: refusal.code, Reason: refusal.reason}
	}
	return nil
}
