package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Ownership tests.
//
// Every case is a refusal or a proven derivation, so each one asserts the error class
// rather than a message: a caller branches on the sentinel, and a test that matched text
// would keep passing after the meaning changed.

type fakeOwnershipReader struct {
	workflows map[domain.WorkflowID]domain.Workflow
	tasks     map[domain.WorkflowID]map[domain.WorkflowTaskID]domain.WorkflowTask
	attempts  map[domain.WorkflowAttemptID]domain.WorkflowAttempt
	repos     map[domain.WorkflowID][]domain.WorkflowRepository
	taskRepos map[domain.WorkflowTaskID][]domain.WorkflowTaskRepository
	err       error
}

func newOwnershipReader() *fakeOwnershipReader {
	return &fakeOwnershipReader{
		workflows: make(map[domain.WorkflowID]domain.Workflow),
		tasks:     make(map[domain.WorkflowID]map[domain.WorkflowTaskID]domain.WorkflowTask),
		attempts:  make(map[domain.WorkflowAttemptID]domain.WorkflowAttempt),
		repos:     make(map[domain.WorkflowID][]domain.WorkflowRepository),
		taskRepos: make(map[domain.WorkflowTaskID][]domain.WorkflowTaskRepository),
	}
}

func (f *fakeOwnershipReader) GetWorkflow(
	_ context.Context,
	id domain.WorkflowID,
) (domain.Workflow, bool, error) {
	if f.err != nil {
		return domain.Workflow{}, false, f.err
	}
	wf, ok := f.workflows[id]
	return wf, ok, nil
}

func (f *fakeOwnershipReader) GetWorkflowTask(
	_ context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) (domain.WorkflowTask, bool, error) {
	if f.err != nil {
		return domain.WorkflowTask{}, false, f.err
	}
	task, ok := f.tasks[workflowID][taskID]
	return task, ok, nil
}

func (f *fakeOwnershipReader) GetWorkflowAttempt(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttempt, bool, error) {
	if f.err != nil {
		return domain.WorkflowAttempt{}, false, f.err
	}
	attempt, ok := f.attempts[attemptID]
	return attempt, ok, nil
}

func (f *fakeOwnershipReader) ListWorkflowRepositories(
	_ context.Context,
	workflowID domain.WorkflowID,
) ([]domain.WorkflowRepository, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.repos[workflowID], nil
}

func (f *fakeOwnershipReader) ListWorkflowTaskRepositories(
	_ context.Context,
	_ domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) ([]domain.WorkflowTaskRepository, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.taskRepos[taskID], nil
}

func (f *fakeOwnershipReader) ListWorkflowAttemptRepoBindings(
	_ context.Context,
	_ domain.WorkflowAttemptID,
) ([]domain.WorkflowAttemptRepoBinding, error) {
	return nil, f.err
}

// seedOwnership builds one workflow with a task, an attempt, and repository scopes.
func seedOwnership(reader *fakeOwnershipReader) {
	workflowID := domain.WorkflowID("wf-1")
	reader.workflows[workflowID] = domain.Workflow{ID: workflowID}
	reader.tasks[workflowID] = map[domain.WorkflowTaskID]domain.WorkflowTask{
		"task-1": {
			ID:           "task-1",
			WorkflowID:   workflowID,
			AgentHarness: domain.HarnessCodex,
		},
	}
	reader.attempts["attempt-1"] = domain.WorkflowAttempt{
		ID:         "attempt-1",
		WorkflowID: workflowID,
		TaskID:     "task-1",
	}
	reader.repos[workflowID] = []domain.WorkflowRepository{
		{
			WorkflowID: workflowID, RepoKey: "backend", ProjectID: "proj-a",
			BaseSHA: "base-a",
		},
		{
			WorkflowID: workflowID, RepoKey: "frontend", ProjectID: "proj-a",
			BaseSHA: "base-b",
		},
	}
	reader.taskRepos["task-1"] = []domain.WorkflowTaskRepository{
		{WorkflowID: workflowID, TaskID: "task-1", RepoKey: "backend", BaseSHA: "task-base-a"},
		{WorkflowID: workflowID, TaskID: "task-1", RepoKey: "frontend"},
	}
}

func TestResolveAttemptOwnershipHappyPath(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)

	ownership, err := ResolveAttemptOwnership(
		context.Background(), reader, "wf-1", "attempt-1")
	if err != nil {
		t.Fatalf("ResolveAttemptOwnership: %v", err)
	}
	if ownership.Attempt.ID != "attempt-1" || ownership.Task.ID != "task-1" {
		t.Fatalf("identity was not resolved: %+v", ownership)
	}
	if ownership.ProjectID != "proj-a" {
		t.Fatalf("project = %q, want proj-a", ownership.ProjectID)
	}
	if ownership.Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want the task's harness", ownership.Harness)
	}
	if len(ownership.Repos) != 2 {
		t.Fatalf("repos = %d, want one binding per task repository scope", len(ownership.Repos))
	}
	for _, repo := range ownership.Repos {
		if repo.AttemptID != "attempt-1" || repo.WorkflowID != "wf-1" {
			t.Fatalf("repo binding was not attributed: %+v", repo)
		}
		if repo.ProjectID != "proj-a" {
			t.Fatalf("repo %s project = %q, want the workflow's project", repo.RepoKey, repo.ProjectID)
		}
		if repo.State != domain.WorkflowRepoBindingStatePending {
			t.Fatalf("repo %s state = %q, want pending", repo.RepoKey, repo.State)
		}
	}
	// The task's own base sha wins; the workflow's is the fallback.
	baseByKey := map[domain.WorkflowRepoKey]string{}
	for _, repo := range ownership.Repos {
		baseByKey[repo.RepoKey] = repo.ExpectedBaseSHA
	}
	if baseByKey["backend"] != "task-base-a" {
		t.Fatalf("backend base = %q, want the task's base", baseByKey["backend"])
	}
	if baseByKey["frontend"] != "base-b" {
		t.Fatalf("frontend base = %q, want the workflow's fallback base", baseByKey["frontend"])
	}
}

func TestResolveAttemptOwnershipRefusesAnUnknownAttempt(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)

	_, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-missing")
	if !errors.Is(err, ErrAttemptNotFound) {
		t.Fatalf("err = %v, want ErrAttemptNotFound", err)
	}
}

func TestResolveAttemptOwnershipRefusesAWorkflowMismatch(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	other := domain.WorkflowID("wf-other")
	reader.workflows[other] = domain.Workflow{ID: other}

	_, err := ResolveAttemptOwnership(context.Background(), reader, other, "attempt-1")
	if !errors.Is(err, ErrWorkflowMismatch) {
		t.Fatalf("err = %v, want ErrWorkflowMismatch", err)
	}
	// The refusal must also be visible as the pre-existing collaboration sentinel, so a
	// caller already matching that keeps working.
	if !errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
		t.Fatalf("err = %v, want it to wrap domain.ErrCollaborationCrossWorkflow", err)
	}
}

func TestResolveAttemptOwnershipRefusesATaskFromAnotherWorkflow(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	// The attempt names a task that does not exist in this workflow.
	reader.attempts["attempt-2"] = domain.WorkflowAttempt{
		ID: "attempt-2", WorkflowID: "wf-1", TaskID: "task-other",
	}

	_, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-2")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestResolveAttemptOwnershipRefusesATaskWorkflowMismatch(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	// The task row itself reports a different workflow: a store that returned it for this
	// workflow would be lying, and the mismatch must be caught rather than trusted.
	task := reader.tasks["wf-1"]["task-1"]
	task.WorkflowID = "wf-somebody-else"
	reader.tasks["wf-1"]["task-1"] = task

	_, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-1")
	if !errors.Is(err, ErrWorkflowMismatch) {
		t.Fatalf("err = %v, want ErrWorkflowMismatch", err)
	}
}

func TestResolveAttemptOwnershipMultiProjectKeepsPerRepoProjects(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	// The second repo lives in a different project: a multi-project task is legal, and
	// each binding must keep its own project id.
	reader.repos["wf-1"] = []domain.WorkflowRepository{
		{WorkflowID: "wf-1", RepoKey: "backend", ProjectID: "proj-a", BaseSHA: "base-a"},
		{WorkflowID: "wf-1", RepoKey: "frontend", ProjectID: "proj-b", BaseSHA: "base-b"},
	}

	ownership, err := ResolveAttemptOwnership(
		context.Background(), reader, "wf-1", "attempt-1")
	if err != nil {
		t.Fatalf("ResolveAttemptOwnership: %v", err)
	}
	// The session's project is chosen deterministically: the first repo by scope order.
	if ownership.ProjectID != "proj-a" {
		t.Fatalf("project = %q, want the first repo's project", ownership.ProjectID)
	}
	projects := map[domain.WorkflowRepoKey]domain.ProjectID{}
	for _, repo := range ownership.Repos {
		projects[repo.RepoKey] = repo.ProjectID
	}
	if projects["backend"] != "proj-a" || projects["frontend"] != "proj-b" {
		t.Fatalf("per-repo projects were collapsed: %v", projects)
	}
}

func TestResolveAttemptOwnershipFallsBackToWorkflowRepos(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	// A task with no repository scope: the workflow's repositories are the scope.
	reader.taskRepos["task-1"] = nil

	ownership, err := ResolveAttemptOwnership(
		context.Background(), reader, "wf-1", "attempt-1")
	if err != nil {
		t.Fatalf("ResolveAttemptOwnership: %v", err)
	}
	if len(ownership.Repos) != 2 {
		t.Fatalf("repos = %d, want the workflow's two repositories", len(ownership.Repos))
	}
	if ownership.ProjectID != "proj-a" {
		t.Fatalf("project = %q, want proj-a", ownership.ProjectID)
	}
}

func TestResolveAttemptOwnershipRefusesAnEmptyRepoScope(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	reader.taskRepos["task-1"] = nil
	reader.repos["wf-1"] = nil

	ownership, err := ResolveAttemptOwnership(
		context.Background(), reader, "wf-1", "attempt-1")
	if !errors.Is(err, ErrProjectUnresolved) {
		t.Fatalf("err = %v, want ErrProjectUnresolved", err)
	}
	if ownership.ProjectID != "" {
		t.Fatalf("a refused resolution returned project %q", ownership.ProjectID)
	}
}

func TestResolveAttemptOwnershipRefusesATaskScopeOutsideTheWorkflow(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	reader.taskRepos["task-1"] = []domain.WorkflowTaskRepository{
		{WorkflowID: "wf-1", TaskID: "task-1", RepoKey: "not-registered"},
	}

	_, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-1")
	if !errors.Is(err, ErrProjectUnresolved) {
		t.Fatalf("err = %v, want ErrProjectUnresolved", err)
	}
}

func TestResolveAttemptOwnershipRefusesAMissingHarness(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	task := reader.tasks["wf-1"]["task-1"]
	task.AgentHarness = ""
	reader.tasks["wf-1"]["task-1"] = task

	_, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-1")
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
}

func TestResolveAttemptOwnershipPropagatesReaderFailures(t *testing.T) {
	reader := newOwnershipReader()
	seedOwnership(reader)
	cause := errors.New("store exploded")
	reader.err = cause

	_, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-1")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the reader's own failure", err)
	}
}

func TestResolveAttemptOwnershipRefusesAnEmptyRequest(t *testing.T) {
	reader := newOwnershipReader()
	for _, tc := range []struct {
		name       string
		workflowID domain.WorkflowID
		attemptID  domain.WorkflowAttemptID
	}{
		{name: "no workflow", attemptID: "attempt-1"},
		{name: "no attempt", workflowID: "wf-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveAttemptOwnership(context.Background(), reader, tc.workflowID, tc.attemptID)
			if !errors.Is(err, ports.ErrExecutionRefused) {
				t.Fatalf("err = %v, want ErrExecutionRefused", err)
			}
		})
	}
}
