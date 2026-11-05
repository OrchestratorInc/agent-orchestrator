package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func workflowServiceTime() time.Time {
	return time.Date(2026, time.November, 3, 13, 0, 0, 0, time.UTC)
}

func newWorkflowService(t *testing.T) (*workflow.Service, *sqlite.Store) {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	now := workflowServiceTime()
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "project-1", Path: "C:/repos/project-1", RegisteredAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	nextID := 0
	nextToken := 0
	service := workflow.New(workflow.Deps{
		Store:     store,
		Admission: sqlitetest.NewAdmissionFixture(t, store, func() time.Time { return now }),
		Clock:     func() time.Time { return now },
		NewID: func() string {
			nextID++
			return fmt.Sprintf("id-%d", nextID)
		},
		NewLeaseToken: func() (string, error) {
			nextToken++
			return fmt.Sprintf("lease-token-%d", nextToken), nil
		},
		GraphLimits:   domain.WorkflowGraphLimits{MaxTasks: 10, MaxDepth: 5, MaxFanOut: 3},
		LeaseDuration: time.Minute,
	})
	return service, store
}

func validWorkflowCreateInput() workflow.CreateInput {
	return workflow.CreateInput{
		Title: "  Multi-agent feature  ", Goal: "  Implement the workflow backend  ",
		Repositories: []workflow.RepositoryInput{{
			RepoKey: "backend", ProjectID: "project-1", PathRef: "backend//internal",
			BaseSHA: "base-sha", TargetRef: "main",
		}},
		Tasks: []workflow.TaskInput{
			{ID: "task-a", Kind: domain.WorkflowTaskKindDevelopment, Title: " Implement domain "},
			{ID: "task-b", Kind: domain.WorkflowTaskKindTesting, Title: " Verify domain ", MaxAttempts: 2},
		},
		Dependencies: []workflow.DependencyInput{{FromTaskID: "task-a", ToTaskID: "task-b"}},
		TaskRepositories: []workflow.TaskRepositoryInput{{
			TaskID: "task-a", RepoKey: "backend", AllowedPaths: []string{"backend//internal", "docs"},
		}},
	}
}

func requireAPIError(t *testing.T, err error, code string) *apierr.Error {
	t.Helper()
	var apiError *apierr.Error
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %T %v, want *apierr.Error", err, err)
	}
	if apiError.Code != code {
		t.Fatalf("API error code = %q, want %q: %v", apiError.Code, code, err)
	}
	return apiError
}

func TestCreateValidatesGraphAndPersistsCanonicalPaths(t *testing.T) {
	ctx := context.Background()
	service, store := newWorkflowService(t)
	created, inserted, err := service.Create(ctx, "create-key", validWorkflowCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !inserted || created.State != domain.WorkflowStateDraft || created.Revision != 1 {
		t.Fatalf("created workflow = %+v inserted=%v", created, inserted)
	}

	repositories, err := store.ListWorkflowRepositories(ctx, created.ID)
	if err != nil || len(repositories) != 1 || repositories[0].PathRef != "backend/internal" {
		t.Fatalf("canonical repositories = %+v err=%v", repositories, err)
	}
	bindings, err := store.ListWorkflowTaskRepositories(ctx, created.ID, "task-a")
	if err != nil || len(bindings) != 1 || len(bindings[0].AllowedPaths) != 2 ||
		bindings[0].AllowedPaths[0] != "backend/internal" {
		t.Fatalf("canonical task repository bindings = %+v err=%v", bindings, err)
	}

	replayed, inserted, err := service.Create(ctx, "create-key", validWorkflowCreateInput())
	if err != nil || inserted || replayed.ID != created.ID {
		t.Fatalf("idempotent Create replay = %+v inserted=%v err=%v", replayed, inserted, err)
	}
}

func TestCreateRejectsTraversalAndCyclesBeforePersistence(t *testing.T) {
	ctx := context.Background()
	service, store := newWorkflowService(t)

	traversal := validWorkflowCreateInput()
	traversal.Repositories[0].PathRef = "src/../outside"
	_, _, err := service.Create(ctx, "traversal-key", traversal)
	requireAPIError(t, err, "INVALID_WORKFLOW_GRAPH")

	cycle := validWorkflowCreateInput()
	cycle.Dependencies = []workflow.DependencyInput{
		{FromTaskID: "task-a", ToTaskID: "task-b"},
		{FromTaskID: "task-b", ToTaskID: "task-a"},
	}
	_, _, err = service.Create(ctx, "cycle-key", cycle)
	requireAPIError(t, err, "INVALID_WORKFLOW_GRAPH")

	items, err := store.ListWorkflows(ctx, "", 20, 0)
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid graphs persisted %d workflows, err=%v", len(items), err)
	}
}

func TestCreateRejectsUnknownOrArchivedProject(t *testing.T) {
	ctx := context.Background()
	service, store := newWorkflowService(t)
	input := validWorkflowCreateInput()
	input.Repositories[0].ProjectID = "missing-project"
	_, _, err := service.Create(ctx, "missing-project-key", input)
	requireAPIError(t, err, "WORKFLOW_PROJECT_NOT_FOUND")

	created, inserted, err := service.Create(ctx, "archived-project-key", validWorkflowCreateInput())
	if err != nil || !inserted {
		t.Fatalf("create before archive = %+v inserted=%v err=%v", created, inserted, err)
	}
	if _, err := store.ArchiveProject(ctx, "project-1", workflowServiceTime().Add(time.Minute)); err != nil {
		t.Fatalf("archive project: %v", err)
	}
	replayed, inserted, err := service.Create(ctx, "archived-project-key", validWorkflowCreateInput())
	if err != nil || inserted || replayed.ID != created.ID {
		t.Fatalf("idempotent replay after project archive = %+v inserted=%v err=%v", replayed, inserted, err)
	}
	_, _, err = service.Create(ctx, "new-archived-project-key", validWorkflowCreateInput())
	requireAPIError(t, err, "WORKFLOW_PROJECT_NOT_FOUND")
}

func TestLifecycleReplaysActivationAndClaimWithoutCreatingDuplicates(t *testing.T) {
	ctx := context.Background()
	service, store := newWorkflowService(t)
	created, _, err := service.Create(ctx, "lifecycle-create", validWorkflowCreateInput())
	if err != nil {
		t.Fatal(err)
	}

	activated, applied, err := service.Activate(ctx, created.ID, created.Revision, "activate-key")
	if err != nil || !applied || activated.State != domain.WorkflowStateActive {
		t.Fatalf("Activate = %+v applied=%v err=%v", activated, applied, err)
	}
	activationReplay, applied, err := service.Activate(ctx, created.ID, created.Revision, "activate-key")
	if err != nil || applied || activationReplay.ID != created.ID {
		t.Fatalf("activation replay = %+v applied=%v err=%v", activationReplay, applied, err)
	}

	task, err := service.GetTask(ctx, created.ID, "task-a")
	if err != nil || task.State != domain.WorkflowTaskStateReady {
		t.Fatalf("ready task = %+v err=%v", task, err)
	}
	claimInput := workflow.ClaimInput{
		WorkflowID: created.ID, TaskID: task.ID, ExpectedRevision: task.Revision,
		WorkerID: "worker-1", IdempotencyKey: "claim-key",
	}
	claimed, claimCreated, err := service.Claim(ctx, claimInput)
	if err != nil || !claimCreated || claimed.Attempt.State != domain.WorkflowAttemptStateClaimed ||
		claimed.LeaseToken != "lease-token-1" {
		t.Fatalf("Claim = %+v created=%v err=%v", claimed, claimCreated, err)
	}
	claimReplay, claimCreated, err := service.Claim(ctx, claimInput)
	if err != nil || claimCreated || claimReplay.Attempt.ID != claimed.Attempt.ID || claimReplay.LeaseToken != claimed.LeaseToken {
		t.Fatalf("claim replay = %+v created=%v err=%v", claimReplay, claimCreated, err)
	}
	payload, err := json.Marshal(claimed)
	if err != nil {
		t.Fatalf("marshal claim result: %v", err)
	}
	var response struct {
		Attempt    map[string]json.RawMessage `json:"attempt"`
		LeaseToken string                     `json:"leaseToken"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("unmarshal claim result: %v", err)
	}
	if response.LeaseToken != claimed.LeaseToken {
		t.Fatalf("claim response lease token = %q, want issued token", response.LeaseToken)
	}
	if _, leaked := response.Attempt["leaseToken"]; leaked {
		t.Fatal("lease token leaked through the general attempt representation")
	}
	attempts, err := store.ListWorkflowAttempts(ctx, created.ID, task.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts after replay = %d err=%v", len(attempts), err)
	}
}

func TestLifecycleStartsSettlesAndRetriesThroughService(t *testing.T) {
	ctx := context.Background()
	service, store := newWorkflowService(t)
	input := validWorkflowCreateInput()
	input.Tasks = []workflow.TaskInput{{ID: "task-a", Kind: domain.WorkflowTaskKindDevelopment, Title: "Implement", MaxAttempts: 2}}
	input.Dependencies = nil
	input.TaskRepositories = nil
	created, _, err := service.Create(ctx, "service-lifecycle-create", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Activate(ctx, created.ID, created.Revision, "service-lifecycle-activate"); err != nil {
		t.Fatal(err)
	}
	task, err := service.GetTask(ctx, created.ID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	claim, _, err := service.Claim(ctx, workflow.ClaimInput{
		WorkflowID: created.ID, TaskID: task.ID, ExpectedRevision: task.Revision,
		WorkerID: "worker-1", IdempotencyKey: "service-claim-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	startInput := workflow.StartInput{
		WorkflowID: created.ID, AttemptID: claim.Attempt.ID,
		LeaseToken: claim.LeaseToken, IdempotencyKey: "service-start-1",
	}
	started, applied, err := service.Start(sqlitetest.ExecutionContext(t, ctx, store, claim.Attempt.ID, workflowServiceTime()), startInput)
	if err != nil || !applied || started.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("Start = %+v applied=%v err=%v", started, applied, err)
	}
	startReplay, applied, err := service.Start(sqlitetest.ExecutionContext(t, ctx, store, claim.Attempt.ID, workflowServiceTime()), startInput)
	if err != nil || applied || startReplay.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("Start replay = %+v applied=%v err=%v", startReplay, applied, err)
	}

	settleInput := workflow.SettleInput{
		WorkflowID: created.ID, AttemptID: claim.Attempt.ID, LeaseToken: claim.LeaseToken,
		Result: domain.WorkflowAttemptStateFailed, ErrorCode: "test_failure",
		IdempotencyKey: "service-settle-1",
	}
	failed, applied, err := service.Settle(sqlitetest.ExecutionContext(t, ctx, store, claim.Attempt.ID, workflowServiceTime()), settleInput)
	if err != nil || !applied || failed.State != domain.WorkflowAttemptStateFailed {
		t.Fatalf("Settle failed = %+v applied=%v err=%v", failed, applied, err)
	}
	settleReplay, applied, err := service.Settle(sqlitetest.ExecutionContext(t, ctx, store, claim.Attempt.ID, workflowServiceTime()), settleInput)
	if err != nil || applied || settleReplay.State != domain.WorkflowAttemptStateFailed {
		t.Fatalf("Settle replay = %+v applied=%v err=%v", settleReplay, applied, err)
	}
	retryWait, err := service.GetTask(ctx, created.ID, "task-a")
	if err != nil || retryWait.State != domain.WorkflowTaskStateRetryWait {
		t.Fatalf("retry_wait task = %+v err=%v", retryWait, err)
	}
	retried, applied, err := service.Retry(ctx, workflow.RetryInput{
		WorkflowID: created.ID, TaskID: retryWait.ID, ExpectedRevision: retryWait.Revision,
		IdempotencyKey: "service-retry",
	})
	if err != nil || !applied || retried.State != domain.WorkflowTaskStateReady {
		t.Fatalf("Retry = %+v applied=%v err=%v", retried, applied, err)
	}
}

func TestLifecycleMapsStaleRevisionAndLeaseErrors(t *testing.T) {
	ctx := context.Background()
	service, _ := newWorkflowService(t)
	created, _, err := service.Create(ctx, "stale-create", validWorkflowCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Activate(ctx, created.ID, created.Revision+1, "stale-activate"); err == nil {
		t.Fatal("activation with stale revision succeeded")
	} else {
		requireAPIError(t, err, "WORKFLOW_REVISION_CONFLICT")
	}
	if _, _, err := service.Claim(ctx, workflow.ClaimInput{
		WorkflowID: created.ID, TaskID: "task-a", ExpectedRevision: 2,
		WorkerID: "worker-1", IdempotencyKey: "before-activation",
	}); err == nil {
		t.Fatal("claim from draft workflow succeeded")
	} else {
		requireAPIError(t, err, "WORKFLOW_TASK_NOT_CLAIMABLE")
	}
}

func TestCreateRejectsOversizedIdempotencyKeyAndMissingConfiguration(t *testing.T) {
	ctx := context.Background()
	service, store := newWorkflowService(t)
	if _, _, err := service.Create(ctx, strings.Repeat("k", 201), validWorkflowCreateInput()); err == nil {
		t.Fatal("oversized idempotency key accepted")
	} else {
		requireAPIError(t, err, "INVALID_IDEMPOTENCY_KEY")
	}

	unconfigured := workflow.New(workflow.Deps{Store: nil})
	if _, _, err := unconfigured.Create(ctx, "valid-key", validWorkflowCreateInput()); err == nil {
		t.Fatal("missing store accepted")
	} else {
		requireAPIError(t, err, "WORKFLOW_STORE_UNAVAILABLE")
	}
	missingLimits := workflow.New(workflow.Deps{Store: store})
	if _, _, err := missingLimits.Create(ctx, "missing-limits-key", validWorkflowCreateInput()); err == nil {
		t.Fatal("missing graph limits accepted")
	} else {
		requireAPIError(t, err, "WORKFLOW_LIMITS_NOT_CONFIGURED")
	}
}

// newWorkflowServiceWithClock returns a service whose clock the test can move,
// so lease expiry can be exercised without sleeping.
func newWorkflowServiceWithClock(t *testing.T) (*workflow.Service, *sqlite.Store, *time.Time) {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "project-1", Path: "C:/repos/project-1", RegisteredAt: workflowServiceTime(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	clock := workflowServiceTime()
	nextID := 0
	nextToken := 0
	service := workflow.New(workflow.Deps{
		Store:     store,
		Admission: sqlitetest.NewAdmissionFixture(t, store, func() time.Time { return clock }),
		Clock:     func() time.Time { return clock },
		NewID: func() string {
			nextID++
			return fmt.Sprintf("id-%d", nextID)
		},
		NewLeaseToken: func() (string, error) {
			nextToken++
			return fmt.Sprintf("lease-token-%d", nextToken), nil
		},
		GraphLimits:   domain.WorkflowGraphLimits{MaxTasks: 10, MaxDepth: 5, MaxFanOut: 3},
		LeaseDuration: time.Minute,
	})
	return service, store, &clock
}

func singleTaskCreateInput() workflow.CreateInput {
	input := validWorkflowCreateInput()
	input.Tasks = []workflow.TaskInput{{
		ID: "task-a", Kind: domain.WorkflowTaskKindDevelopment, Title: "Implement", MaxAttempts: 2,
	}}
	input.Dependencies = nil
	input.TaskRepositories = nil
	return input
}

// Recovery turns the attempt UNKNOWN and revokes its lease token. Replaying the
// original claim must surface a typed conflict instead of a token-free result.
func TestClaimReplayAfterRecoveryReportsExpiredClaim(t *testing.T) {
	ctx := context.Background()
	service, _, clock := newWorkflowServiceWithClock(t)
	created, _, err := service.Create(ctx, "replay-create", singleTaskCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Activate(ctx, created.ID, created.Revision, "replay-activate"); err != nil {
		t.Fatal(err)
	}
	task, err := service.GetTask(ctx, created.ID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	claimInput := workflow.ClaimInput{
		WorkflowID: created.ID, TaskID: task.ID, ExpectedRevision: task.Revision,
		WorkerID: "worker-1", IdempotencyKey: "replay-claim-key",
	}
	claim, createdAttempt, err := service.Claim(ctx, claimInput)
	if err != nil || !createdAttempt || claim.LeaseToken == "" {
		t.Fatalf("Claim = %+v created=%v err=%v", claim, createdAttempt, err)
	}

	*clock = clock.Add(2 * time.Minute)
	recovered, err := service.RecoverExpired(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverExpired = %d err=%v, want 1", recovered, err)
	}

	replayed, createdAttempt, err := service.Claim(ctx, claimInput)
	if err == nil {
		t.Fatalf("claim replay after recovery = %+v created=%v, want a typed conflict", replayed, createdAttempt)
	}
	requireAPIError(t, err, "WORKFLOW_CLAIM_REPLAY_EXPIRED")
}

// A recovered attempt leaves its task blocked: the service must refuse both
// Start and Settle on it even though the workflow itself is still active.
func TestRecoveredAttemptRejectsStartAndSettle(t *testing.T) {
	ctx := context.Background()
	service, store, clock := newWorkflowServiceWithClock(t)
	created, _, err := service.Create(ctx, "recover-create", singleTaskCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Activate(ctx, created.ID, created.Revision, "recover-activate"); err != nil {
		t.Fatal(err)
	}
	task, err := service.GetTask(ctx, created.ID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	claim, _, err := service.Claim(ctx, workflow.ClaimInput{
		WorkflowID: created.ID, TaskID: task.ID, ExpectedRevision: task.Revision,
		WorkerID: "worker-1", IdempotencyKey: "recover-claim",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 保留恢复前的执行快照，证明旧执行者不能在租约撤销后继续写入。
	ctx = sqlitetest.ExecutionContext(t, ctx, store, claim.Attempt.ID, *clock)
	*clock = clock.Add(2 * time.Minute)
	recovered, err := service.RecoverExpired(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverExpired = %d err=%v, want 1", recovered, err)
	}
	blocked, err := service.GetTask(ctx, created.ID, task.ID)
	if err != nil || blocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("recovered task = %+v err=%v, want blocked", blocked, err)
	}
	attempts, err := store.ListWorkflowAttempts(ctx, created.ID, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("recovered attempts = %+v err=%v, want one unknown attempt", attempts, err)
	}

	if _, _, err := service.Start(ctx, workflow.StartInput{
		WorkflowID: created.ID, AttemptID: claim.Attempt.ID, LeaseToken: claim.LeaseToken,
		IdempotencyKey: "recover-start",
	}); err == nil {
		t.Fatal("Start on a recovered attempt succeeded")
	} else {
		requireAPIError(t, err, "WORKFLOW_ADMISSION_DENIED")
	}
	if _, _, err := service.Settle(ctx, workflow.SettleInput{
		WorkflowID: created.ID, AttemptID: claim.Attempt.ID, LeaseToken: claim.LeaseToken,
		Result: domain.WorkflowAttemptStateSucceeded, IdempotencyKey: "recover-settle",
	}); err == nil {
		t.Fatal("Settle on a recovered attempt succeeded")
	} else {
		requireAPIError(t, err, "WORKFLOW_ADMISSION_DENIED")
	}
}

// GetDefinition must return every persisted child row, and tasks must keep the
// store's priority-descending order rather than an insertion order.
func TestGetDefinitionReturnsCompleteGraph(t *testing.T) {
	ctx := context.Background()
	service, _ := newWorkflowService(t)
	input := validWorkflowCreateInput()
	// The store orders tasks by priority DESC, created_at, id; distinct
	// priorities prove the definition preserves that order.
	input.Tasks[1].Priority = 5
	created, _, err := service.Create(ctx, "definition-create", input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	definition, err := service.GetDefinition(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDefinition: %v", err)
	}
	if definition.Workflow.ID != created.ID || definition.Workflow.Title != created.Title {
		t.Fatalf("definition workflow = %+v, want %+v", definition.Workflow, created)
	}
	if len(definition.Repositories) != 1 || definition.Repositories[0].PathRef != "backend/internal" {
		t.Fatalf("definition repositories = %+v", definition.Repositories)
	}
	if len(definition.Tasks) != 2 || definition.Tasks[0].ID != "task-b" || definition.Tasks[1].ID != "task-a" {
		t.Fatalf("definition tasks = %+v, want task-b then task-a", definition.Tasks)
	}
	if len(definition.Edges) != 1 || definition.Edges[0].FromTaskID != "task-a" || definition.Edges[0].ToTaskID != "task-b" {
		t.Fatalf("definition edges = %+v", definition.Edges)
	}
	if len(definition.TaskRepositories) != 1 {
		t.Fatalf("definition task repositories = %+v", definition.TaskRepositories)
	}
	binding := definition.TaskRepositories[0]
	if binding.TaskID != "task-a" || binding.AllowedPaths == nil || len(binding.AllowedPaths) != 2 ||
		binding.AllowedPaths[0] != "backend/internal" {
		t.Fatalf("definition task repository binding = %+v", binding)
	}
}

func TestGetDefinitionMissingWorkflow(t *testing.T) {
	service, _ := newWorkflowService(t)
	_, err := service.GetDefinition(context.Background(), "workflow-missing")
	requireAPIError(t, err, "WORKFLOW_NOT_FOUND")
}

// The generated TypeScript types expect arrays, so absent children must encode
// as [] rather than null.
func TestGetDefinitionEmptySlicesAreNotNull(t *testing.T) {
	ctx := context.Background()
	service, _ := newWorkflowService(t)
	input := validWorkflowCreateInput()
	input.Dependencies = nil
	input.TaskRepositories = nil
	created, _, err := service.Create(ctx, "empty-children-create", input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	definition, err := service.GetDefinition(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDefinition: %v", err)
	}
	if definition.Repositories == nil {
		t.Fatal("definition repositories is nil")
	}
	if definition.Edges == nil {
		t.Fatal("definition edges is nil")
	}
	if definition.TaskRepositories == nil {
		t.Fatal("definition task repositories is nil")
	}
	if len(definition.Edges) != 0 || len(definition.TaskRepositories) != 0 {
		t.Fatalf("definition children = %d edges, %d task repositories, want none",
			len(definition.Edges), len(definition.TaskRepositories))
	}
	payload, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	var encoded struct {
		Edges            *[]json.RawMessage `json:"edges"`
		TaskRepositories *[]json.RawMessage `json:"taskRepositories"`
	}
	if err := json.Unmarshal(payload, &encoded); err != nil {
		t.Fatalf("unmarshal definition: %v", err)
	}
	if encoded.Edges == nil || len(*encoded.Edges) != 0 {
		t.Fatalf("encoded edges = %v, want []", encoded.Edges)
	}
	if encoded.TaskRepositories == nil || len(*encoded.TaskRepositories) != 0 {
		t.Fatalf("encoded taskRepositories = %v, want []", encoded.TaskRepositories)
	}
}

// The full operator path out of the fail-closed UNKNOWN state: an expired lease
// blocks the task, and only an explicit resolve lets it reach a terminal state.
// Nothing in this path re-dispatches work.
func TestResolveReleasesBlockedTaskThroughService(t *testing.T) {
	ctx := context.Background()
	service, store, clock := newWorkflowServiceWithClock(t)
	created, _, err := service.Create(ctx, "resolve-create", singleTaskCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Activate(ctx, created.ID, created.Revision, "resolve-activate"); err != nil {
		t.Fatal(err)
	}
	task, err := service.GetTask(ctx, created.ID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Claim(ctx, workflow.ClaimInput{
		WorkflowID: created.ID, TaskID: task.ID, ExpectedRevision: task.Revision,
		WorkerID: "worker-1", IdempotencyKey: "resolve-claim",
	}); err != nil {
		t.Fatal(err)
	}

	*clock = clock.Add(2 * time.Minute)
	if recovered, err := service.RecoverExpired(ctx); err != nil || recovered != 1 {
		t.Fatalf("RecoverExpired = %d err=%v, want 1", recovered, err)
	}
	blocked, err := service.GetTask(ctx, created.ID, task.ID)
	if err != nil || blocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("task after recovery = %+v err=%v, want blocked", blocked, err)
	}
	attempts, err := store.ListWorkflowAttempts(ctx, created.ID, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("attempts after recovery = %+v err=%v, want one unknown", attempts, err)
	}

	resolved, applied, err := service.Resolve(ctx, workflow.ResolveInput{
		WorkflowID: created.ID, AttemptID: attempts[0].ID,
		Outcome: domain.WorkflowAttemptStateSucceeded, ExpectedTaskRevision: blocked.Revision,
		IdempotencyKey: "resolve-key",
	})
	if err != nil || !applied {
		t.Fatalf("Resolve = %+v applied=%v err=%v", resolved, applied, err)
	}
	if resolved.State != domain.WorkflowAttemptStateSucceeded || resolved.EndedAt == nil {
		t.Fatalf("resolved attempt = %+v, want terminal succeeded", resolved)
	}
	succeeded, err := service.GetTask(ctx, created.ID, task.ID)
	if err != nil || succeeded.State != domain.WorkflowTaskStateSucceeded {
		t.Fatalf("task after resolve = %+v err=%v, want succeeded", succeeded, err)
	}
	// Single task workflow: resolving the only task completes the workflow.
	workflowRecord, err := service.Get(ctx, created.ID)
	if err != nil || workflowRecord.State != domain.WorkflowStateCompleted {
		t.Fatalf("workflow after resolve = %+v err=%v, want completed", workflowRecord, err)
	}

	// A replayed resolve is a no-op, not a second transition.
	replay, applied, err := service.Resolve(ctx, workflow.ResolveInput{
		WorkflowID: created.ID, AttemptID: attempts[0].ID,
		Outcome: domain.WorkflowAttemptStateSucceeded, ExpectedTaskRevision: blocked.Revision,
		IdempotencyKey: "resolve-key",
	})
	if err != nil || applied || replay.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("replayed Resolve = %+v applied=%v err=%v", replay, applied, err)
	}
}

// Only a decided outcome can be recorded, and the request must be identifiable
// by an idempotency key before any store work happens.
func TestResolveRejectsInvalidOutcomeAndKey(t *testing.T) {
	ctx := context.Background()
	service, _ := newWorkflowService(t)
	for _, outcome := range []domain.WorkflowAttemptState{
		domain.WorkflowAttemptStateUnknown, domain.WorkflowAttemptStateRunning,
		domain.WorkflowAttemptStateClaimed, domain.WorkflowAttemptStateSettling,
	} {
		_, _, err := service.Resolve(ctx, workflow.ResolveInput{
			WorkflowID: "workflow-x", AttemptID: "attempt-x", Outcome: outcome,
			IdempotencyKey: "resolve-invalid",
		})
		if err == nil {
			t.Fatalf("Resolve accepted outcome %q", outcome)
		}
		requireAPIError(t, err, "INVALID_ATTEMPT_RESULT")
	}
	for _, key := range []string{"", "   ", strings.Repeat("k", 201)} {
		_, _, err := service.Resolve(ctx, workflow.ResolveInput{
			WorkflowID: "workflow-x", AttemptID: "attempt-x",
			Outcome: domain.WorkflowAttemptStateSucceeded, IdempotencyKey: key,
		})
		if err == nil {
			t.Fatalf("Resolve accepted idempotency key %q", key)
		}
		requireAPIError(t, err, "INVALID_IDEMPOTENCY_KEY")
	}

	unconfigured := workflow.New(workflow.Deps{Store: nil})
	if _, _, err := unconfigured.Resolve(ctx, workflow.ResolveInput{
		WorkflowID: "workflow-x", AttemptID: "attempt-x",
		Outcome: domain.WorkflowAttemptStateSucceeded, IdempotencyKey: "resolve-nil-store",
	}); err == nil {
		t.Fatal("Resolve with a nil store succeeded")
	} else {
		requireAPIError(t, err, "WORKFLOW_STORE_UNAVAILABLE")
	}
}
