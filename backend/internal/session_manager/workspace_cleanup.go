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
	switch {
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
	for index, command := range project.Config.PreRemove {
		if strings.TrimSpace(command) == "" {
			continue
		}
		out := &cleanupOutput{}
		env := agentlaunch.MergeEnv(project.Config.Env, map[string]string{
			"AO_SOURCE_TREE_PATH": project.Path,
			"AO_WORKTREE_PATH":    workspacePath,
		})
		err := runWorkspaceStep(ctx, workspacePath, command, env, out)
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
	return err
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
func (m *Manager) recordWorkspaceCleanup(ctx context.Context, rec domain.SessionRecord, disposition domain.WorkspaceDisposition, code string) error {
	// Record cancellation too, while shutdown is draining background workers.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalIntentBudget)
	defer cancel()
	now := time.Now().UTC()
	facts, ok, err := m.store.GetSessionCleanupFacts(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !ok || facts.SessionGeneration != rec.CleanupGeneration {
		facts = domain.SessionCleanupRecord{SessionID: rec.ID, SessionGeneration: rec.CleanupGeneration, RuntimeReleasedAt: now}
	}
	if disposition == domain.DispositionPending {
		facts.AttemptCount++
		facts.LastAttemptAt = now
	}
	facts.WorkspaceDisposition, facts.FailureCode = disposition, code
	facts.NextAttemptAt = time.Time{}
	return m.store.UpsertSessionCleanupFacts(ctx, facts)
}

func (m *Manager) recordWorkspaceCleanupResult(ctx context.Context, rec domain.SessionRecord, reason string) error {
	disposition, code := domain.DispositionRemoved, ""
	if reason != "" {
		disposition, code = domain.DispositionFailed, "WORKSPACE_CLEANUP_FAILED"
		if reason == "workspace has uncommitted changes" {
			disposition, code = domain.DispositionPreservedDirty, "WORKSPACE_DIRTY"
		}
	}
	return m.recordWorkspaceCleanup(ctx, rec, disposition, code)
}
