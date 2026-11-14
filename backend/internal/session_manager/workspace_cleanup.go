package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

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

type stepTimeoutError struct {
	timeout time.Duration
	cause   error
}

func (e *stepTimeoutError) Error() string { return "timed out after " + stepMinutes(e.timeout) }
func (e *stepTimeoutError) Unwrap() error { return e.cause }

func (m *Manager) runPreRemove(projectID domain.ProjectID, workspacePath string) error {
	return m.runPreRemoveContext(m.backgroundContext, projectID, workspacePath)
}

func (m *Manager) runReplacementPreRemove(id domain.SessionID, projectID domain.ProjectID, workspacePath string) {
	ctx, cancel := context.WithTimeout(m.backgroundContext, replacementCleanupBudget)
	defer cancel()
	if err := m.runPreRemoveContext(ctx, projectID, workspacePath); err != nil {
		m.logger.Warn("retire replacement: workspace cleanup failed; removing workspace anyway", "sessionID", id, "error", err)
	}
}

var replacementCleanupBudget = killTeardownBudget

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

var runCleanupStep = runWorkspaceStep

func runWorkspaceStep(ctx context.Context, workspacePath, command string, env map[string]string, output io.Writer) error {
	cmd := workspaceStepCommand(ctx, command)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir = workspacePath
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		return nil
	}
	if exitedCleanlyAtCancel(ctx, cmd.ProcessState, err) {
		return nil
	}
	return err
}

func exitedCleanlyAtCancel(ctx context.Context, state *os.ProcessState, err error) bool {
	return err != nil && state != nil && state.Success() && ctx.Err() != nil && errors.Is(err, ctx.Err())
}

type cleanupOutput struct{ tail []byte }

func (o *cleanupOutput) Write(p []byte) (int, error) {
	n := len(p)
	o.tail = append(o.tail, p...)
	if len(o.tail) > 4096 {
		o.tail = append([]byte(nil), o.tail[len(o.tail)-4096:]...)
	}
	return n, nil
}

func (m *Manager) recordWorkspaceCleanup(ctx context.Context, rec domain.SessionRecord, disposition domain.WorkspaceDisposition, code string, reportOnly bool) error {
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
		disposition = domain.DispositionPending
	case refusal.code == domain.CleanupSkipDirty:
		disposition, code = domain.DispositionPreservedDirty, "WORKSPACE_DIRTY"
	case refusal.refused():
		disposition, code = domain.DispositionFailed, "WORKSPACE_CLEANUP_FAILED"
	}
	return m.recordWorkspaceCleanup(ctx, rec, disposition, code, reportOnly)
}

type cleanupRefusal struct {
	code   domain.CleanupSkipCode
	reason string
}

func (r cleanupRefusal) refused() bool { return r.code != "" }

var (
	refusedDirty             = cleanupRefusal{domain.CleanupSkipDirty, "workspace has uncommitted changes"}
	refusedInUse             = cleanupRefusal{domain.CleanupSkipInUse, "workspace in use by a live session"}
	refusedShellTerminalOpen = cleanupRefusal{domain.CleanupSkipShellTerminalOpen, "shell terminal still open"}
	refusedAlreadyRunning    = cleanupRefusal{domain.CleanupSkipAlreadyRunning, "workspace cleanup is already running"}
	refusedAttachmentsFailed = cleanupRefusal{domain.CleanupSkipAttachmentsFailed, "attachment preservation failed"}
	refusedTeardownFailed    = cleanupRefusal{domain.CleanupSkipTeardownFailed, "workspace teardown failed"}
)

func (m *Manager) recordSettledWorkspaceCleanup(ctx context.Context, rec domain.SessionRecord, disposition domain.WorkspaceDisposition) {
	if err := m.recordWorkspaceCleanup(ctx, rec, disposition, "", false); err != nil {
		m.logger.Warn("record workspace cleanup", "sessionID", rec.ID, "disposition", disposition, "error", err)
	}
}

func (m *Manager) workspaceCleanupSettled(ctx context.Context, rec domain.SessionRecord) bool {
	facts, ok, err := m.store.GetSessionCleanupFacts(ctx, rec.ID)
	if err != nil {
		m.logger.Warn("read workspace cleanup facts", "sessionID", rec.ID, "error", err)
		return false
	}
	return ok && releasedForGeneration(rec, facts)
}

type cleanupFactsBatchStore interface {
	ListSessionCleanupFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionCleanupRecord, error)
}

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

type cleanupCandidateStore interface {
	ListTerminalCleanupCandidates(ctx context.Context, now time.Time) ([]domain.SessionID, error)
}

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
	rows, err := m.store.ListSessionWorktrees(ctx, id)
	if err != nil || len(rows) > 0 {
		return err
	}
	if path := rec.Metadata.WorkspacePath; path != "" {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil
		}
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
	rec, ok, err = m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated || rec.Metadata.WorkspacePath != "" {
		return err
	}
	m.cleanupSystemPromptDir(rec.ID)
	return m.recordWorkspaceCleanup(ctx, rec, domain.DispositionNotApplicable, "", false)
}

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
	ErrSessionNotTerminated = errors.New("session: not terminated")
	ErrWorkspaceNotReleased = errors.New("session: workspace not released")
)

type CleanupRefusedError struct {
	Code   domain.CleanupSkipCode
	Reason string
}

func (e *CleanupRefusedError) Error() string { return "workspace cleanup refused: " + e.Reason }

type terminatedSessionDeleter interface {
	DeleteTerminatedSession(ctx context.Context, id domain.SessionID) (bool, error)
}

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
	if m.usageFlusher != nil {
		m.usageFlusher.Flush(ctx)
	}
	isFork := m.isSessionFork(ctx, id)
	var forkNative string
	if isFork {
		forkNative = m.forkNativeSessionFile(ctx, rec)
	}
	deleted, err := deleter.DeleteTerminatedSession(ctx, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if !deleted {
		return fmt.Errorf("delete %s: %w", id, ErrNotFound)
	}
	if isFork {
		m.cleanupForkLeftovers(rec, forkNative)
	}
	m.cleanupSystemPromptDir(id)
	m.cleanupAttachments(ctx, id)
	return nil
}

func (m *Manager) cleanupBeforeDelete(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated || rec.Metadata.WorkspacePath == "" || m.workspaceCleanupSettled(ctx, rec) {
		return err
	}
	if h := runtimeHandle(rec.Metadata); h.ID != "" {
		_ = m.runtime.Destroy(ctx, h)
	}
	if _, refusal := m.cleanupWorkspaceUnderGate(ctx, rec, workspaceInfo(rec)); refusal.refused() {
		return &CleanupRefusedError{Code: refusal.code, Reason: refusal.reason}
	}
	return nil
}
