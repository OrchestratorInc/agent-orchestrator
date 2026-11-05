package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// HTTP bind ownership.
//
// A bind request arrives with a workflow id, a task id, and an attempt id, and every
// one of them is caller-supplied. Before M2 nothing proved they described the same
// piece of work, so a request could name one workflow's task and another workflow's
// attempt and still be accepted. ResolveAttemptOwnership is the single place that
// cross-checks them against durable rows and derives the facts a spawn needs.
//
// The rules are deliberately refusals rather than repairs: a request whose ids
// disagree is a caller bug, and guessing which one meant it would bind a session to
// work nobody asked for.

// Ownership errors. They are separate from the gateway's execution outcomes because
// they describe a malformed request, not an external owner's answer.
var (
	// ErrAttemptNotFound means no attempt row exists for the requested id.
	ErrAttemptNotFound = errors.New("workflow attempt not found")
	// ErrTaskNotFound means no task row exists inside the named workflow.
	ErrTaskNotFound = errors.New("workflow task not found")
	// ErrWorkflowNotFound means no workflow row exists for the requested id.
	ErrWorkflowNotFound = errors.New("workflow not found")
	// ErrWorkflowMismatch means the attempt or task belongs to a different workflow
	// than the one the request named. Refusals wrap it together with
	// domain.ErrCollaborationCrossWorkflow, so a caller matching either sentinel sees
	// the same cause.
	ErrWorkflowMismatch = errors.New("workflow ownership mismatch")
	// ErrProjectUnresolved means the task's repository scope names no resolvable
	// project, so a session could not be attributed to one. It is a refusal, never a
	// default: an empty ProjectID on a session makes it untraceable.
	ErrProjectUnresolved = errors.New("workflow task repository scope has no project")
)

// OwnershipReader is the durable read surface ownership resolution needs. It is
// declared here so the HTTP controller can be tested against a fake without the SQLite
// store, and so this package depends on six reads rather than whole store.
type OwnershipReader interface {
	GetWorkflow(ctx context.Context, id domain.WorkflowID) (domain.Workflow, bool, error)
	GetWorkflowTask(
		ctx context.Context,
		workflowID domain.WorkflowID,
		taskID domain.WorkflowTaskID,
	) (domain.WorkflowTask, bool, error)
	GetWorkflowAttempt(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttempt, bool, error)
	ListWorkflowRepositories(
		ctx context.Context,
		workflowID domain.WorkflowID,
	) ([]domain.WorkflowRepository, error)
	ListWorkflowTaskRepositories(
		ctx context.Context,
		workflowID domain.WorkflowID,
		taskID domain.WorkflowTaskID,
	) ([]domain.WorkflowTaskRepository, error)
	ListWorkflowAttemptRepoBindings(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) ([]domain.WorkflowAttemptRepoBinding, error)
}

// AttemptOwnership is the proven identity of one attempt's work.
//
// ProjectID is the session's project and Harness is the agent it must run: both are
// derived from durable rows and neither is ever invented. Repos is already shaped as
// repo bindings so the caller can persist them without re-deriving per-repository
// project ids.
type AttemptOwnership struct {
	Attempt   domain.WorkflowAttempt
	Task      domain.WorkflowTask
	Repos     []domain.WorkflowAttemptRepoBinding
	ProjectID domain.ProjectID
	Harness   domain.AgentHarness
}

// ResolveAttemptOwnership proves that the workflow, task, and attempt ids describe one
// piece of work, and derives the project, harness, and per-repository bindings for it.
//
// The checks run in order of what a caller can fix:
//
//  1. the attempt exists (unknown attempt);
//  2. the attempt belongs to the named workflow and task (cross-workflow refusal);
//  3. the workflow and task exist and agree (unknown workflow/task, mismatch);
//  4. the task's repository scope resolves to exactly one project, or the workflow's
//     does (every binding keeps its own project id regardless);
//  5. the task names a harness.
func ResolveAttemptOwnership(
	ctx context.Context,
	reader OwnershipReader,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
) (AttemptOwnership, error) {
	if reader == nil {
		return AttemptOwnership{}, fmt.Errorf("%w: no ownership reader", ports.ErrExecutionRefused)
	}
	if strings.TrimSpace(string(workflowID)) == "" ||
		strings.TrimSpace(string(attemptID)) == "" {
		return AttemptOwnership{}, fmt.Errorf(
			"%w: a bind must name a workflow and an attempt", ports.ErrExecutionRefused)
	}

	attempt, found, err := reader.GetWorkflowAttempt(ctx, attemptID)
	if err != nil {
		return AttemptOwnership{}, err
	}
	if !found {
		return AttemptOwnership{}, fmt.Errorf("%w: %s", ErrAttemptNotFound, attemptID)
	}
	// The attempt is the row the caller will bind a session to, so its own workflow is
	// the authority. Checking it before reading the workflow means a cross-workflow
	// request is refused without touching the other workflow's rows.
	if attempt.WorkflowID != workflowID {
		return AttemptOwnership{}, fmt.Errorf(
			"%w: %w: attempt %s belongs to workflow %s, not %s",
			ErrWorkflowMismatch, domain.ErrCollaborationCrossWorkflow,
			attemptID, attempt.WorkflowID, workflowID)
	}

	workflow, found, err := reader.GetWorkflow(ctx, workflowID)
	if err != nil {
		return AttemptOwnership{}, err
	}
	if !found {
		return AttemptOwnership{}, fmt.Errorf("%w: %s", ErrWorkflowNotFound, workflowID)
	}
	_ = workflow

	task, found, err := reader.GetWorkflowTask(ctx, workflowID, attempt.TaskID)
	if err != nil {
		return AttemptOwnership{}, err
	}
	if !found {
		return AttemptOwnership{}, fmt.Errorf(
			"%w: %s/%s", ErrTaskNotFound, workflowID, attempt.TaskID)
	}
	if task.WorkflowID != workflowID {
		return AttemptOwnership{}, fmt.Errorf(
			"%w: %w: task %s belongs to workflow %s, not %s",
			ErrWorkflowMismatch, domain.ErrCollaborationCrossWorkflow,
			task.ID, task.WorkflowID, workflowID)
	}

	taskRepos, err := reader.ListWorkflowTaskRepositories(ctx, workflowID, task.ID)
	if err != nil {
		return AttemptOwnership{}, err
	}
	workflowRepos, err := reader.ListWorkflowRepositories(ctx, workflowID)
	if err != nil {
		return AttemptOwnership{}, err
	}

	projectID, repos, err := resolveRepos(taskRepos, workflowRepos)
	if err != nil {
		return AttemptOwnership{}, err
	}

	harness := task.AgentHarness
	if strings.TrimSpace(string(harness)) == "" {
		// Guessing a harness would launch an agent the workflow never chose, and the
		// project/role default is a Session Manager decision this boundary cannot see.
		return AttemptOwnership{}, fmt.Errorf(
			"%w: task %s names no agent harness", ports.ErrExecutionRefused, task.ID)
	}

	// Every repository binding is rebuilt from the task scope, not read from existing
	// rows: this function describes what the attempt SHOULD bind, and reusing stale
	// rows would carry another attempt's observations into this one.
	for i := range repos {
		repos[i].AttemptID = attempt.ID
		repos[i].WorkflowID = workflowID
		repos[i].State = domain.WorkflowRepoBindingStatePending
	}

	return AttemptOwnership{
		Attempt:   attempt,
		Task:      task,
		Repos:     repos,
		ProjectID: projectID,
		Harness:   harness,
	}, nil
}

// resolveRepos builds one repo binding per task repository scope entry, and picks the
// session's project.
//
// The project rule: every repo in the task scope must be resolvable to a project. When
// the scope spans several projects, the first by repo key is chosen deterministically
// and each binding keeps its own project id, so a multi-project task still records
// where each worktree belongs. A task with no scope falls back to the workflow's
// repositories. Nothing resolvable is a refusal.
func resolveRepos(
	taskRepos []domain.WorkflowTaskRepository,
	workflowRepos []domain.WorkflowRepository,
) (domain.ProjectID, []domain.WorkflowAttemptRepoBinding, error) {
	byKey := make(map[domain.WorkflowRepoKey]domain.WorkflowRepository, len(workflowRepos))
	for _, repo := range workflowRepos {
		byKey[repo.RepoKey] = repo
	}

	if len(taskRepos) == 0 {
		// No task scope: the workflow's own repository list is the scope, so every
		// registered repo becomes a binding with the workflow's base sha.
		if len(workflowRepos) == 0 {
			return "", nil, fmt.Errorf(
				"%w: neither the task nor the workflow registers a repository", ErrProjectUnresolved)
		}
		repos := make([]domain.WorkflowAttemptRepoBinding, 0, len(workflowRepos))
		var projectID domain.ProjectID
		for _, repo := range workflowRepos {
			if strings.TrimSpace(string(repo.ProjectID)) == "" {
				return "", nil, fmt.Errorf(
					"%w: workflow repository %s has no project", ErrProjectUnresolved, repo.RepoKey)
			}
			if projectID == "" {
				projectID = repo.ProjectID
			}
			repos = append(repos, domain.WorkflowAttemptRepoBinding{
				RepoKey:         repo.RepoKey,
				ProjectID:       repo.ProjectID,
				ExpectedBaseSHA: repo.BaseSHA,
			})
		}
		return projectID, repos, nil
	}

	repos := make([]domain.WorkflowAttemptRepoBinding, 0, len(taskRepos))
	var projectID domain.ProjectID
	for _, scope := range taskRepos {
		repo, found := byKey[scope.RepoKey]
		if !found {
			return "", nil, fmt.Errorf(
				"%w: task scope names repository %s, which the workflow does not register",
				ErrProjectUnresolved, scope.RepoKey)
		}
		if strings.TrimSpace(string(repo.ProjectID)) == "" {
			return "", nil, fmt.Errorf(
				"%w: workflow repository %s has no project", ErrProjectUnresolved, scope.RepoKey)
		}
		if projectID == "" {
			projectID = repo.ProjectID
		}
		baseSHA := scope.BaseSHA
		if strings.TrimSpace(baseSHA) == "" {
			baseSHA = repo.BaseSHA
		}
		repos = append(repos, domain.WorkflowAttemptRepoBinding{
			RepoKey:         scope.RepoKey,
			ProjectID:       repo.ProjectID,
			ExpectedBaseSHA: baseSHA,
		})
	}
	return projectID, repos, nil
}
