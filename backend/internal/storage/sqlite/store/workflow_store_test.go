package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func workflowStoreTime() time.Time {
	return time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
}

func workflowStoreRecord(id domain.WorkflowID, now time.Time) domain.Workflow {
	return domain.Workflow{
		ID: id, Title: "Workflow test", Goal: "Verify durable workflow lifecycle",
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func workflowStoreTask(id domain.WorkflowTaskID, workflowID domain.WorkflowID, maxAttempts int64, now time.Time) domain.WorkflowTask {
	return domain.WorkflowTask{
		ID: id, WorkflowID: workflowID, Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Implement " + string(id), State: domain.WorkflowTaskStatePending,
		MaxAttempts: maxAttempts, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func workflowStoreAttempt(id domain.WorkflowAttemptID, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID, workerID, token string, now, leaseUntil time.Time) domain.WorkflowAttempt {
	return domain.WorkflowAttempt{
		ID: id, WorkflowID: workflowID, TaskID: taskID, WorkerID: workerID,
		LeaseToken: token, LeaseUntil: &leaseUntil, State: domain.WorkflowAttemptStateClaimed,
		CreatedAt: now, UpdatedAt: now,
	}
}

func createWorkflowForStoreTest(
	t *testing.T,
	s *sqlite.Store,
	id domain.WorkflowID,
	tasks []domain.WorkflowTask,
	edges []domain.WorkflowEdge,
	key string,
	fingerprint string,
	now time.Time,
) (domain.Workflow, bool) {
	t.Helper()
	created, inserted, err := s.CreateWorkflow(
		context.Background(), workflowStoreRecord(id, now), nil, tasks, edges, nil, key, fingerprint,
	)
	if err != nil {
		t.Fatalf("CreateWorkflow(%s): %v", id, err)
	}
	return created, inserted
}

func TestWorkflowStoreCreatePersistsGraphAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "workflow-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-create-1")
	repositories := []domain.WorkflowRepository{{
		WorkflowID: workflowID, RepoKey: "backend", ProjectID: "workflow-project",
		BaseSHA: "base-sha", TargetRef: "main",
	}}
	tasks := []domain.WorkflowTask{
		workflowStoreTask("task-a", workflowID, 2, now),
		workflowStoreTask("task-b", workflowID, 1, now),
	}
	edges := []domain.WorkflowEdge{{
		WorkflowID: workflowID, FromTaskID: "task-a", ToTaskID: "task-b",
	}}
	taskRepositories := []domain.WorkflowTaskRepository{{
		WorkflowID: workflowID, TaskID: "task-a", RepoKey: "backend",
		AllowedPaths: []string{"backend/internal", "docs"}, BaseSHA: "task-base",
	}}
	fingerprint := strings.Repeat("a", 64)

	created, inserted, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(workflowID, now), repositories, tasks, edges, taskRepositories,
		"create-key", fingerprint,
	)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if !inserted || created.ID != workflowID {
		t.Fatalf("create result = %+v inserted=%v, want fresh workflow %s", created, inserted, workflowID)
	}

	gotWorkflow, found, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || !found || gotWorkflow.State != domain.WorkflowStateDraft {
		t.Fatalf("GetWorkflow = %+v found=%v err=%v", gotWorkflow, found, err)
	}
	gotTasks, err := s.ListWorkflowTasks(ctx, workflowID, "", 20, 0)
	if err != nil || len(gotTasks) != 2 {
		t.Fatalf("ListWorkflowTasks = %d tasks, err=%v", len(gotTasks), err)
	}
	gotEdges, err := s.ListWorkflowEdges(ctx, workflowID)
	if err != nil || len(gotEdges) != 1 || gotEdges[0].FromTaskID != "task-a" || gotEdges[0].ToTaskID != "task-b" {
		t.Fatalf("ListWorkflowEdges = %+v err=%v", gotEdges, err)
	}
	gotBindings, err := s.ListWorkflowTaskRepositories(ctx, workflowID, "task-a")
	if err != nil || len(gotBindings) != 1 ||
		len(gotBindings[0].AllowedPaths) != 2 || gotBindings[0].AllowedPaths[0] != "backend/internal" {
		t.Fatalf("ListWorkflowTaskRepositories = %+v err=%v", gotBindings, err)
	}

	replayID := domain.WorkflowID("workflow-create-replay")
	replayed, inserted, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(replayID, now), nil, nil, nil, nil, "create-key", fingerprint,
	)
	if err != nil || inserted || replayed.ID != workflowID {
		t.Fatalf("idempotent replay = %+v inserted=%v err=%v, want original workflow", replayed, inserted, err)
	}
	if _, found, err := s.GetWorkflow(ctx, replayID); err != nil || found {
		t.Fatalf("replay created a second workflow: found=%v err=%v", found, err)
	}

	_, _, err = s.CreateWorkflow(
		ctx, workflowStoreRecord(replayID, now), nil, nil, nil, nil, "create-key", strings.Repeat("b", 64),
	)
	if !errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		t.Fatalf("reusing key with another fingerprint error = %v, want idempotency conflict", err)
	}
}

func TestWorkflowStoreCreateRejectsArchivedProjectWithinTransaction(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "archived-workflow-project")
	if _, err := s.ArchiveProject(ctx, "archived-workflow-project", workflowStoreTime().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-archived-project")
	repository := domain.WorkflowRepository{
		WorkflowID: workflowID, RepoKey: "backend", ProjectID: "archived-workflow-project",
	}
	task := workflowStoreTask("task-a", workflowID, 1, now)
	key, fingerprint := "archived-project-create", strings.Repeat("a", 64)
	_, _, err := s.CreateWorkflow(ctx, workflowStoreRecord(workflowID, now),
		[]domain.WorkflowRepository{repository}, []domain.WorkflowTask{task}, nil, nil, key, fingerprint)
	if !errors.Is(err, domain.ErrWorkflowProjectArchived) {
		t.Fatalf("create with archived project error = %v, want archived project error", err)
	}
	if _, found, readErr := s.GetWorkflow(ctx, workflowID); readErr != nil || found {
		t.Fatalf("archived project failure left workflow: found=%v err=%v", found, readErr)
	}
	if _, found, readErr := s.GetWorkflowByIdempotencyKey(ctx, key, fingerprint); readErr != nil || found {
		t.Fatalf("archived project failure left idempotency reservation: found=%v err=%v", found, readErr)
	}
}

func TestWorkflowStoreCreateRollsBackIdempotencyReservationWithGraph(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "workflow-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-rollback")
	task := workflowStoreTask("task-a", workflowID, 1, now)
	invalidBinding := domain.WorkflowTaskRepository{
		WorkflowID: workflowID, TaskID: task.ID, RepoKey: "missing-repo",
		AllowedPaths: []string{"src"},
	}
	key, fingerprint := "rollback-key", strings.Repeat("c", 64)

	_, _, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(workflowID, now), nil, []domain.WorkflowTask{task},
		nil, []domain.WorkflowTaskRepository{invalidBinding}, key, fingerprint,
	)
	if err == nil {
		t.Fatal("CreateWorkflow accepted a task binding to an unknown repository")
	}
	if _, found, readErr := s.GetWorkflow(ctx, workflowID); readErr != nil || found {
		t.Fatalf("failed transaction left workflow behind: found=%v err=%v", found, readErr)
	}

	created, inserted, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(workflowID, now), nil, []domain.WorkflowTask{task},
		nil, nil, key, fingerprint,
	)
	if err != nil || !inserted || created.ID != workflowID {
		t.Fatalf("retry after rolled-back reservation = %+v inserted=%v err=%v", created, inserted, err)
	}
}

func TestWorkflowLifecycleRetriesAndCompletesWithIdempotentAttempts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-lifecycle")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
		"lifecycle-create", strings.Repeat("1", 64), now)

	activated, applied, err := s.ActivateWorkflow(
		ctx, workflowID, 1, "activate-key", strings.Repeat("2", 64), now.Add(time.Second),
	)
	if err != nil || !applied || activated.State != domain.WorkflowStateActive || activated.Revision != 2 {
		t.Fatalf("ActivateWorkflow = %+v applied=%v err=%v", activated, applied, err)
	}
	ready, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || ready.State != domain.WorkflowTaskStateReady || ready.Revision != 2 {
		t.Fatalf("task after activation = %+v found=%v err=%v", ready, found, err)
	}
	replayedActivation, applied, err := s.ActivateWorkflow(
		ctx, workflowID, 1, "activate-key", strings.Repeat("2", 64), now.Add(time.Second),
	)
	if err != nil || applied || replayedActivation.ID != workflowID {
		t.Fatalf("activation replay = %+v applied=%v err=%v", replayedActivation, applied, err)
	}

	leaseUntil1 := now.Add(2 * time.Minute)
	attempt1 := workflowStoreAttempt("attempt-1", workflowID, taskID, "worker-1", "lease-1", now.Add(2*time.Second), leaseUntil1)
	claimed1, created, err := claimWithAdmission(t, s,
		ctx, attempt1, ready.Revision, "claim-key-1", strings.Repeat("3", 64),
	)
	if err != nil || !created || claimed1.AttemptNo != 1 || claimed1.State != domain.WorkflowAttemptStateClaimed {
		t.Fatalf("first claim = %+v created=%v err=%v", claimed1, created, err)
	}
	replayedClaim, created, err := claimWithAdmission(t, s,
		ctx, attempt1, ready.Revision, "claim-key-1", strings.Repeat("3", 64),
	)
	if err != nil || created || replayedClaim.ID != claimed1.ID {
		t.Fatalf("claim replay = %+v created=%v err=%v", replayedClaim, created, err)
	}

	started1, applied, err := s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed1.ID, now.Add(3*time.Second)), workflowID, claimed1.ID, "lease-1", "start-key-1", strings.Repeat("4", 64), now.Add(3*time.Second),
	)
	if err != nil || !applied || started1.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("start attempt 1 = %+v applied=%v err=%v", started1, applied, err)
	}
	replayedStart, applied, err := s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed1.ID, now.Add(3*time.Second)), workflowID, claimed1.ID, "lease-1", "start-key-1", strings.Repeat("4", 64), now.Add(3*time.Second),
	)
	if err != nil || applied || replayedStart.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("start replay = %+v applied=%v err=%v", replayedStart, applied, err)
	}

	failed1, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed1.ID, now.Add(4*time.Second)), workflowID, claimed1.ID, "lease-1", domain.WorkflowAttemptStateFailed,
		"test_failure", "settle-key-1", strings.Repeat("5", 64), now.Add(4*time.Second),
	)
	if err != nil || !applied || failed1.State != domain.WorkflowAttemptStateFailed {
		t.Fatalf("settle failed attempt = %+v applied=%v err=%v", failed1, applied, err)
	}
	retryWait, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || retryWait.State != domain.WorkflowTaskStateRetryWait {
		t.Fatalf("task after failed attempt = %+v found=%v err=%v", retryWait, found, err)
	}
	replayedSettle, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed1.ID, now.Add(4*time.Second)), workflowID, claimed1.ID, "lease-1", domain.WorkflowAttemptStateFailed,
		"test_failure", "settle-key-1", strings.Repeat("5", 64), now.Add(4*time.Second),
	)
	if err != nil || applied || replayedSettle.State != domain.WorkflowAttemptStateFailed {
		t.Fatalf("settle replay = %+v applied=%v err=%v", replayedSettle, applied, err)
	}

	retried, applied, err := s.RetryWorkflowTask(
		ctx, workflowID, taskID, retryWait.Revision, "retry-key", strings.Repeat("6", 64), now.Add(5*time.Second),
	)
	if err != nil || !applied || retried.State != domain.WorkflowTaskStateReady {
		t.Fatalf("RetryWorkflowTask = %+v applied=%v err=%v", retried, applied, err)
	}
	retryReplay, applied, err := s.RetryWorkflowTask(
		ctx, workflowID, taskID, retryWait.Revision, "retry-key", strings.Repeat("6", 64), now.Add(5*time.Second),
	)
	if err != nil || applied || retryReplay.State != domain.WorkflowTaskStateReady {
		t.Fatalf("RetryWorkflowTask replay = %+v applied=%v err=%v", retryReplay, applied, err)
	}
	leaseUntil2 := now.Add(3 * time.Minute)
	attempt2 := workflowStoreAttempt("attempt-2", workflowID, taskID, "worker-2", "lease-2", now.Add(6*time.Second), leaseUntil2)
	claimed2, created, err := claimWithAdmission(t, s,
		ctx, attempt2, retried.Revision, "claim-key-2", strings.Repeat("7", 64),
	)
	if err != nil || !created || claimed2.AttemptNo != 2 {
		t.Fatalf("second claim = %+v created=%v err=%v", claimed2, created, err)
	}
	if _, applied, err := s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed2.ID, now.Add(7*time.Second)), workflowID, claimed2.ID, "lease-2", "start-key-2", strings.Repeat("8", 64), now.Add(7*time.Second),
	); err != nil || !applied {
		t.Fatalf("start attempt 2: applied=%v err=%v", applied, err)
	}
	succeeded, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed2.ID, now.Add(8*time.Second)), workflowID, claimed2.ID, "lease-2", domain.WorkflowAttemptStateSucceeded,
		"", "settle-key-2", strings.Repeat("9", 64), now.Add(8*time.Second),
	)
	if err != nil || !applied || succeeded.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("settle successful attempt = %+v applied=%v err=%v", succeeded, applied, err)
	}
	finalTask, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || finalTask.State != domain.WorkflowTaskStateSucceeded {
		t.Fatalf("final task = %+v found=%v err=%v", finalTask, found, err)
	}
	finalWorkflow, found, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || !found || finalWorkflow.State != domain.WorkflowStateCompleted {
		t.Fatalf("final workflow = %+v found=%v err=%v", finalWorkflow, found, err)
	}
	attempts, err := s.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("attempt history = %d, err=%v", len(attempts), err)
	}
}

func TestWorkflowClaimIsSingleWinnerAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	first, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open primary store: %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close primary store: %v", err)
		}
	})
	second, err := sqlite.OpenPreMigrated(dataDir)
	if err != nil {
		t.Fatalf("open second store instance: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second store: %v", err)
		}
	})
	seedProject(t, first, "workflow-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-concurrent")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, first, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
		"concurrent-create", strings.Repeat("a", 64), now)
	_, _, err = first.ActivateWorkflow(
		ctx, workflowID, 1, "concurrent-activate", strings.Repeat("b", 64), now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := first.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}

	stores := []*sqlite.Store{first, second}
	results := make([]domain.WorkflowAttempt, 2)
	created := make([]bool, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			nowForAttempt := now.Add(time.Duration(2+i) * time.Second)
			leaseUntil := nowForAttempt.Add(time.Minute)
			attempt := workflowStoreAttempt(
				domain.WorkflowAttemptID("attempt-racer-"+string(rune('1'+i))),
				workflowID, taskID, "worker-"+string(rune('1'+i)), "lease-"+string(rune('1'+i)),
				nowForAttempt, leaseUntil,
			)
			results[i], created[i], errs[i] = claimWithAdmission(t, stores[i],
				ctx, attempt, task.Revision, "claim-racer-"+string(rune('1'+i)),
				strings.Repeat(string(rune('c'+i)), 64),
			)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for i := range errs {
		if errs[i] == nil && created[i] {
			successes++
			continue
		}
		if !errors.Is(errs[i], domain.ErrWorkflowTaskNotClaimable) {
			t.Fatalf("claim racer %d: result=%+v created=%v err=%v", i, results[i], created[i], errs[i])
		}
	}
	if successes != 1 {
		t.Fatalf("successful claimers = %d, want exactly one (results=%+v errors=%v)", successes, results, errs)
	}
	attempts, err := first.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d, err=%v; want one", len(attempts), err)
	}
}

func TestWorkflowDependenciesControlReadyStateAndUnlockOnSuccess(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-dependency-ready")
	taskA := workflowStoreTask("task-a", workflowID, 1, now)
	taskB := workflowStoreTask("task-b", workflowID, 1, now)
	edge := domain.WorkflowEdge{WorkflowID: workflowID, FromTaskID: taskA.ID, ToTaskID: taskB.ID}
	createWorkflowForStoreTest(t, s, workflowID, []domain.WorkflowTask{taskA, taskB}, []domain.WorkflowEdge{edge},
		"dependency-create", strings.Repeat("a", 64), now)

	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "dependency-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	readyA, found, err := s.GetWorkflowTask(ctx, workflowID, taskA.ID)
	if err != nil || !found || readyA.State != domain.WorkflowTaskStateReady {
		t.Fatalf("root task state = %+v found=%v err=%v", readyA, found, err)
	}
	pendingB, found, err := s.GetWorkflowTask(ctx, workflowID, taskB.ID)
	if err != nil || !found || pendingB.State != domain.WorkflowTaskStatePending {
		t.Fatalf("dependent task before predecessor success = %+v found=%v err=%v", pendingB, found, err)
	}

	leaseUntil := now.Add(time.Minute)
	attempt := workflowStoreAttempt("attempt-task-a", workflowID, taskA.ID, "worker-1", "lease-a", now.Add(2*time.Second), leaseUntil)
	claimed, _, err := claimWithAdmission(t, s, ctx, attempt, readyA.Revision, "dependency-claim", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-a", domain.WorkflowAttemptStateSucceeded,
		"", "dependency-settle", strings.Repeat("d", 64), now.Add(3*time.Second),
	); err != nil || !applied {
		t.Fatalf("settle predecessor: applied=%v err=%v", applied, err)
	}
	readyB, found, err := s.GetWorkflowTask(ctx, workflowID, taskB.ID)
	if err != nil || !found || readyB.State != domain.WorkflowTaskStateReady {
		t.Fatalf("dependent task after predecessor success = %+v found=%v err=%v", readyB, found, err)
	}
}

func TestWorkflowRecoveryMarksExpiredAttemptUnknownAndBlocksTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-recovery")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 3, now)}, nil,
		"recovery-create", strings.Repeat("a", 64), now)
	_, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "recovery-activate", strings.Repeat("b", 64), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(10 * time.Second)
	attempt := workflowStoreAttempt("attempt-expired", workflowID, taskID, "worker-expired", "lease-expired", now.Add(2*time.Second), leaseUntil)
	claimed, _, err := claimWithAdmission(t, s, ctx, attempt, task.Revision, "recovery-claim", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}

	expired, err := s.RecoverExpiredWorkflowAttempts(ctx, leaseUntil.Add(time.Second))
	if err != nil || expired != 1 {
		t.Fatalf("first recovery = %d, err=%v; want one expired attempt", expired, err)
	}
	recoveredAttempt, found, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || recoveredAttempt.State != domain.WorkflowAttemptStateUnknown ||
		recoveredAttempt.LeaseToken != "" || recoveredAttempt.LeaseUntil != nil {
		t.Fatalf("recovered attempt = %+v found=%v err=%v", recoveredAttempt, found, err)
	}
	blocked, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || blocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("recovered task = %+v found=%v err=%v", blocked, found, err)
	}
	expired, err = s.RecoverExpiredWorkflowAttempts(ctx, leaseUntil.Add(2*time.Second))
	if err != nil || expired != 0 {
		t.Fatalf("second recovery = %d, err=%v; want idempotent no-op", expired, err)
	}
}

func TestWorkflowClaimRejectsAlreadyExpiredLease(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-expired-lease")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"expired-create", strings.Repeat("a", 64), now)
	_, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "expired-activate", strings.Repeat("b", 64), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(time.Second)
	attempt := workflowStoreAttempt(
		"attempt-expired-before-claim", workflowID, taskID, "worker-1", "lease-1",
		now.Add(2*time.Second), expiredAt,
	)
	_, _, err = claimWithAdmission(t, s, ctx, attempt, task.Revision, "expired-claim", strings.Repeat("c", 64))
	if !errors.Is(err, domain.ErrInvalidWorkflowAttemptLease) {
		t.Fatalf("claim with expired lease error = %v, want invalid lease", err)
	}
	if attempts, listErr := s.ListWorkflowAttempts(ctx, workflowID, taskID); listErr != nil || len(attempts) != 0 {
		t.Fatalf("expired lease created attempts=%d err=%v", len(attempts), listErr)
	}

	validLease := now.Add(time.Minute)
	invalidState := workflowStoreAttempt(
		"attempt-wrong-state", workflowID, taskID, "worker-1", "lease-2",
		now.Add(2*time.Second), validLease,
	)
	invalidState.State = domain.WorkflowAttemptStateRunning
	_, _, err = claimWithAdmission(t, s, ctx, invalidState, task.Revision, "wrong-state-key", strings.Repeat("d", 64))
	if !errors.Is(err, domain.ErrInvalidWorkflowAttempt) {
		t.Fatalf("claim with non-claimed attempt state error = %v, want invalid attempt", err)
	}
	invalidNumber := workflowStoreAttempt(
		"attempt-wrong-number", workflowID, taskID, "worker-1", "lease-3",
		now.Add(2*time.Second), validLease,
	)
	invalidNumber.AttemptNo = 1
	_, _, err = claimWithAdmission(t, s, ctx, invalidNumber, task.Revision, "wrong-number-key", strings.Repeat("e", 64))
	if !errors.Is(err, domain.ErrInvalidWorkflowAttempt) {
		t.Fatalf("claim with caller-supplied attempt number error = %v, want invalid attempt", err)
	}
}

func TestWorkflowFailsWhenTaskExhaustsAttemptBudget(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-terminal-failure")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"terminal-create", strings.Repeat("a", 64), now)
	_, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "terminal-activate", strings.Repeat("b", 64), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(time.Minute)
	attempt := workflowStoreAttempt("attempt-terminal", workflowID, taskID, "worker-1", "lease-1", now.Add(2*time.Second), leaseUntil)
	claimed, _, err := claimWithAdmission(t, s, ctx, attempt, task.Revision, "terminal-claim", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateFailed,
		"permanent_failure", "terminal-settle", strings.Repeat("d", 64), now.Add(3*time.Second),
	); err != nil || !applied {
		t.Fatalf("settle terminal failure: applied=%v err=%v", applied, err)
	}
	failedWorkflow, found, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || !found || failedWorkflow.State != domain.WorkflowStateFailed {
		t.Fatalf("workflow after terminal task failure = %+v found=%v err=%v", failedWorkflow, found, err)
	}
	failedTask, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || failedTask.State != domain.WorkflowTaskStateFailed {
		t.Fatalf("task after terminal failure = %+v found=%v err=%v", failedTask, found, err)
	}
}

func TestWorkflowRecoveryAfterStoreRestartKeepsUnknownAttemptBlocked(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	first, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	current := first
	t.Cleanup(func() {
		if current != nil {
			if err := current.Close(); err != nil {
				t.Errorf("close current store: %v", err)
			}
		}
	})

	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-restart-recovery")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, first, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 3, now)}, nil,
		"restart-create", strings.Repeat("a", 64), now)
	if _, _, err := first.ActivateWorkflow(ctx, workflowID, 1, "restart-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	task, _, err := first.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(10 * time.Second)
	attempt := workflowStoreAttempt(
		"attempt-before-restart", workflowID, taskID, "worker-before-restart", "lease-before-restart",
		now.Add(2*time.Second), leaseUntil,
	)
	claimed, _, err := claimWithAdmission(t, first, ctx, attempt, task.Revision, "restart-claim", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}
	current = nil
	restarted, err := sqlite.OpenPreMigrated(dataDir)
	if err != nil {
		t.Fatalf("reopen persisted database: %v", err)
	}
	current = restarted

	expired, err := restarted.RecoverExpiredWorkflowAttempts(ctx, leaseUntil.Add(time.Second))
	if err != nil || expired != 1 {
		t.Fatalf("recovery after restart = %d err=%v, want one expired attempt", expired, err)
	}
	recovered, found, err := restarted.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || recovered.State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("persisted attempt after restart recovery = %+v found=%v err=%v", recovered, found, err)
	}
	blocked, found, err := restarted.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || blocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("persisted task after restart recovery = %+v found=%v err=%v", blocked, found, err)
	}
}

// A terminal failure of one task moves the workflow to failed. A sibling task
// whose claim was granted while the workflow was still active must still be
// able to start and settle; only NEW work is gated on the active state.
func TestWorkflowTerminalFailureStillDrainsAlreadyClaimedSibling(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-drain-siblings")
	taskA := domain.WorkflowTaskID("task-a")
	taskB := domain.WorkflowTaskID("task-b")
	createWorkflowForStoreTest(t, s, workflowID, []domain.WorkflowTask{
		workflowStoreTask(taskA, workflowID, 1, now),
		workflowStoreTask(taskB, workflowID, 1, now),
	}, nil, "drain-create", strings.Repeat("a", 64), now)

	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "drain-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	readyA, found, err := s.GetWorkflowTask(ctx, workflowID, taskA)
	if err != nil || !found || readyA.State != domain.WorkflowTaskStateReady {
		t.Fatalf("ready task a = %+v found=%v err=%v", readyA, found, err)
	}
	readyB, found, err := s.GetWorkflowTask(ctx, workflowID, taskB)
	if err != nil || !found || readyB.State != domain.WorkflowTaskStateReady {
		t.Fatalf("ready task b = %+v found=%v err=%v", readyB, found, err)
	}

	leaseUntil := now.Add(time.Minute)
	claimedA, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-a", workflowID, taskA, "worker-a", "lease-a", now.Add(2*time.Second), leaseUntil),
		readyA.Revision, "drain-claim-a", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim task a = %+v created=%v err=%v", claimedA, created, err)
	}
	claimedB, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-b", workflowID, taskB, "worker-b", "lease-b", now.Add(2*time.Second), leaseUntil),
		readyB.Revision, "drain-claim-b", strings.Repeat("d", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim task b = %+v created=%v err=%v", claimedB, created, err)
	}

	if _, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimedA.ID, now.Add(3*time.Second)), workflowID, claimedA.ID, "lease-a", domain.WorkflowAttemptStateFailed,
		"permanent_failure", "drain-settle-a", strings.Repeat("e", 64), now.Add(3*time.Second),
	); err != nil || !applied {
		t.Fatalf("settle task a failure: applied=%v err=%v", applied, err)
	}
	failedWorkflow, found, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || !found || failedWorkflow.State != domain.WorkflowStateFailed {
		t.Fatalf("workflow after terminal failure = %+v found=%v err=%v", failedWorkflow, found, err)
	}

	startedB, applied, err := s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimedB.ID, now.Add(4*time.Second)), workflowID, claimedB.ID, "lease-b", "drain-start-b", strings.Repeat("f", 64), now.Add(4*time.Second),
	)
	if err != nil || !applied || startedB.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("start already-claimed sibling = %+v applied=%v err=%v", startedB, applied, err)
	}
	settledB, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimedB.ID, now.Add(5*time.Second)), workflowID, claimedB.ID, "lease-b", domain.WorkflowAttemptStateSucceeded,
		"", "drain-settle-b", strings.Repeat("0", 64), now.Add(5*time.Second),
	)
	if err != nil || !applied || settledB.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("settle sibling = %+v applied=%v err=%v", settledB, applied, err)
	}
	succeededB, found, err := s.GetWorkflowTask(ctx, workflowID, taskB)
	if err != nil || !found || succeededB.State != domain.WorkflowTaskStateSucceeded {
		t.Fatalf("task b after settle = %+v found=%v err=%v", succeededB, found, err)
	}
	stillFailed, found, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || !found || stillFailed.State != domain.WorkflowStateFailed {
		t.Fatalf("workflow after sibling success = %+v found=%v err=%v; want it to stay failed", stillFailed, found, err)
	}
}

// Lease ordering is a TEXT comparison in SQLite, so every instant must be
// stored with one offset. A caller clock in a non-UTC zone must not shift the
// comparison in either direction.
func TestWorkflowLeaseOrderingIsNormalizedToUTC(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	zone := time.FixedZone("UTC+8", 8*3600)

	clockedClaim := func(workflowID domain.WorkflowID, taskID domain.WorkflowTaskID, claimedAt time.Time, suffix string) {
		t.Helper()
		createWorkflowForStoreTest(t, s, workflowID,
			[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
			"zone-create-"+suffix, strings.Repeat("a", 64), now)
		if _, _, err := s.ActivateWorkflow(
			ctx, workflowID, 1, "zone-activate-"+suffix, strings.Repeat("b", 64), now.Add(time.Second),
		); err != nil {
			t.Fatal(err)
		}
		ready, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
		if err != nil || !found {
			t.Fatalf("load task %s: found=%v err=%v", taskID, found, err)
		}
		leaseUntil := claimedAt.Add(30 * time.Second)
		if _, created, err := claimWithAdmission(t, s,
			ctx,
			workflowStoreAttempt("attempt-"+domain.WorkflowAttemptID(suffix), workflowID, taskID, "worker-"+suffix, "lease-"+suffix, claimedAt, leaseUntil),
			ready.Revision, "zone-claim-"+suffix, strings.Repeat("c", 64),
		); err != nil || !created {
			t.Fatalf("claim %s created=%v err=%v", suffix, created, err)
		}
	}

	// One claim written with a UTC+8 clock and one written with UTC: both leases
	// expire 30s after the same instant.
	clockedClaim("workflow-zone-offset", "task-offset", now.Add(2*time.Second).In(zone), "offset")
	clockedClaim("workflow-zone-utc", "task-utc", now.Add(2*time.Second), "utc")

	// 10s after the claim: a UTC+8 recovery clock reads 10s past the hour in its
	// own zone. Comparing raw zone strings would wrongly expire the UTC claim.
	expired, err := s.RecoverExpiredWorkflowAttempts(ctx, now.Add(12*time.Second).In(zone))
	if err != nil || expired != 0 {
		t.Fatalf("recovery before real expiry = %d err=%v; want 0", expired, err)
	}

	// 40s after the claim both leases are past their real expiry even though the
	// UTC+8 claim still sorts before its own local clock text.
	expired, err = s.RecoverExpiredWorkflowAttempts(ctx, now.Add(42*time.Second))
	if err != nil || expired != 2 {
		t.Fatalf("recovery after real expiry = %d err=%v; want 2", expired, err)
	}
}

func TestWorkflowStartReplayWithNewKeyDoesNotConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-start-replay")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
		"start-replay-create", strings.Repeat("a", 64), now)
	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "start-replay-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ready, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("load task: found=%v err=%v", found, err)
	}
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-1", workflowID, taskID, "worker-1", "lease-1", now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "start-replay-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}

	first, applied, err := s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-1", "start-replay-key-1", strings.Repeat("d", 64), now.Add(3*time.Second),
	)
	if err != nil || !applied || first.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("first start = %+v applied=%v err=%v", first, applied, err)
	}
	replayed, applied, err := s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(4*time.Second)), workflowID, claimed.ID, "lease-1", "start-replay-key-2", strings.Repeat("d", 64), now.Add(4*time.Second),
	)
	if err != nil || applied || replayed.State != domain.WorkflowAttemptStateRunning {
		t.Fatalf("start replay with a new key = %+v applied=%v err=%v; want applied=false and no error", replayed, applied, err)
	}
	running, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || running.State != domain.WorkflowTaskStateRunning || running.Revision != ready.Revision+2 {
		t.Fatalf("task after replayed start = %+v found=%v err=%v; want revision %d", running, found, err, ready.Revision+2)
	}
}

func TestWorkflowStartRejectsWrongLeaseToken(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-start-wrong-token")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
		"wrong-token-create", strings.Repeat("a", 64), now)
	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "wrong-token-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-1", workflowID, taskID, "worker-1", "lease-1", now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "wrong-token-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}
	_, _, err = s.StartWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-other", "wrong-token-start", strings.Repeat("d", 64), now.Add(3*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("start with wrong token error = %v, want lease conflict", err)
	}
	unchanged, found, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || unchanged.State != domain.WorkflowAttemptStateClaimed {
		t.Fatalf("attempt after rejected start = %+v found=%v err=%v", unchanged, found, err)
	}
}

func TestWorkflowSettleRejectsWrongLeaseToken(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-settle-wrong-token")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
		"settle-token-create", strings.Repeat("a", 64), now)
	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "settle-token-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-1", workflowID, taskID, "worker-1", "lease-1", now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "settle-token-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}
	_, _, err = s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-other", domain.WorkflowAttemptStateSucceeded,
		"", "settle-token-settle", strings.Repeat("d", 64), now.Add(3*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("settle with wrong token error = %v, want lease conflict", err)
	}
	unchanged, found, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || unchanged.State != domain.WorkflowAttemptStateClaimed {
		t.Fatalf("attempt after rejected settle = %+v found=%v err=%v", unchanged, found, err)
	}
}

func TestWorkflowSettleReplayWithNewKeyReportsCurrentOutcome(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-settle-replay")
	taskID := domain.WorkflowTaskID("task-main")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"settle-replay-create", strings.Repeat("a", 64), now)
	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1, "settle-replay-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-1", workflowID, taskID, "worker-1", "lease-1", now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "settle-replay-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}
	settled, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateSucceeded,
		"", "settle-replay-key-1", strings.Repeat("d", 64), now.Add(3*time.Second),
	)
	if err != nil || !applied || settled.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("first settle = %+v applied=%v err=%v", settled, applied, err)
	}

	replayed, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(4*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateSucceeded,
		"", "settle-replay-key-2", strings.Repeat("d", 64), now.Add(4*time.Second),
	)
	if err != nil || applied || replayed.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("settle replay with a new key = %+v applied=%v err=%v; want applied=false and no error", replayed, applied, err)
	}

	_, _, err = s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(5*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateFailed,
		"late_failure", "settle-replay-key-3", strings.Repeat("e", 64), now.Add(5*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("settle replay with a different result error = %v, want lease conflict", err)
	}
	recorded, found, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || recorded.State != domain.WorkflowAttemptStateSucceeded || recorded.ErrorCode != "" {
		t.Fatalf("attempt after rejected conflicting settle = %+v found=%v err=%v", recorded, found, err)
	}
}

func TestWorkflowStoreCanonicalizesRepositoryPaths(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "workflow-canonical-project")
	now := workflowStoreTime()
	fingerprint := strings.Repeat("a", 64)
	workflowID := domain.WorkflowID("workflow-canonical-paths")
	repository := domain.WorkflowRepository{
		WorkflowID: workflowID, RepoKey: "backend", ProjectID: "workflow-canonical-project",
		PathRef: "a//b",
	}
	task := workflowStoreTask("task-a", workflowID, 1, now)
	binding := domain.WorkflowTaskRepository{
		WorkflowID: workflowID, TaskID: task.ID, RepoKey: "backend",
		AllowedPaths: []string{"docs//guide"},
	}
	if _, inserted, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(workflowID, now), []domain.WorkflowRepository{repository},
		[]domain.WorkflowTask{task}, nil, []domain.WorkflowTaskRepository{binding},
		"canonical-create", fingerprint,
	); err != nil || !inserted {
		t.Fatalf("create canonical workflow: inserted=%v err=%v", inserted, err)
	}
	repositories, err := s.ListWorkflowRepositories(ctx, workflowID)
	if err != nil || len(repositories) != 1 || repositories[0].PathRef != "a/b" {
		t.Fatalf("stored repositories = %+v err=%v; want path_ref a/b", repositories, err)
	}
	bindings, err := s.ListWorkflowTaskRepositories(ctx, workflowID, task.ID)
	if err != nil || len(bindings) != 1 || len(bindings[0].AllowedPaths) != 1 ||
		bindings[0].AllowedPaths[0] != "docs/guide" {
		t.Fatalf("stored task repositories = %+v err=%v; want docs/guide", bindings, err)
	}

	// The same project+path spelled differently must not create a second row.
	duplicateID := domain.WorkflowID("workflow-canonical-duplicate")
	duplicate := []domain.WorkflowRepository{
		{
			WorkflowID: duplicateID, RepoKey: "first", ProjectID: "workflow-canonical-project",
			PathRef: "a//b",
		},
		{
			WorkflowID: duplicateID, RepoKey: "second", ProjectID: "workflow-canonical-project",
			PathRef: "a/b",
		},
	}
	_, _, err = s.CreateWorkflow(
		ctx, workflowStoreRecord(duplicateID, now), duplicate,
		[]domain.WorkflowTask{workflowStoreTask("task-a", duplicateID, 1, now)}, nil, nil,
		"canonical-duplicate", strings.Repeat("b", 64),
	)
	if err == nil {
		t.Fatal("store accepted two spellings of one repository path")
	}
	if _, found, readErr := s.GetWorkflow(ctx, duplicateID); readErr != nil || found {
		t.Fatalf("rejected duplicate left a workflow row: found=%v err=%v", found, readErr)
	}
	if repositories, listErr := s.ListWorkflowRepositories(ctx, workflowID); listErr != nil || len(repositories) != 1 {
		t.Fatalf("repositories after rejected duplicate = %+v err=%v; want exactly one", repositories, listErr)
	}

	invalid := domain.WorkflowRepository{
		WorkflowID: "workflow-canonical-invalid", RepoKey: "backend",
		ProjectID: "workflow-canonical-project", PathRef: "../escape",
	}
	_, _, err = s.CreateWorkflow(
		ctx, workflowStoreRecord("workflow-canonical-invalid", now), []domain.WorkflowRepository{invalid},
		[]domain.WorkflowTask{workflowStoreTask("task-a", "workflow-canonical-invalid", 1, now)}, nil, nil,
		"canonical-invalid", strings.Repeat("c", 64),
	)
	if !errors.Is(err, domain.ErrInvalidWorkflowRepository) {
		t.Fatalf("create with traversing path_ref error = %v, want invalid repository", err)
	}
}

// openWorkflowStorePair opens the primary store and a second instance over the
// same database file, which is how two daemon-size processes would race.
func openWorkflowStorePair(t *testing.T) (*sqlite.Store, *sqlite.Store) {
	t.Helper()
	dataDir := t.TempDir()
	first, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open primary store: %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close primary store: %v", err)
		}
	})
	second, err := sqlite.OpenPreMigrated(dataDir)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second store: %v", err)
		}
	})
	return first, second
}

func activateWorkflowForStoreTest(
	t *testing.T,
	s *sqlite.Store,
	workflowID domain.WorkflowID,
	tasks []domain.WorkflowTask,
	now time.Time,
	key string,
) {
	t.Helper()
	createWorkflowForStoreTest(t, s, workflowID, tasks, nil, key+"-create", strings.Repeat("a", 64), now)
	if _, _, err := s.ActivateWorkflow(context.Background(), workflowID, 1, key+"-activate", strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

// A retried claim that arrives on another store instance with the same key and
// fingerprint must resolve to the one durable attempt, not create a second one.
func TestWorkflowClaimWithSameKeyIsSharedAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	first, second := openWorkflowStorePair(t)
	seedProject(t, first, "workflow-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-claim-shared-key")
	taskID := domain.WorkflowTaskID("task-main")
	activateWorkflowForStoreTest(t, first, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, now, "shared-key")
	task, found, err := first.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("load ready task: found=%v err=%v", found, err)
	}

	stores := []*sqlite.Store{first, second}
	results := make([]domain.WorkflowAttempt, 2)
	created := make([]bool, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			claimAt := now.Add(2 * time.Second)
			attempt := workflowStoreAttempt(
				domain.WorkflowAttemptID("attempt-racer-"+string(rune('1'+i))),
				workflowID, taskID, "worker-shared", "lease-shared", claimAt, claimAt.Add(time.Minute),
			)
			results[i], created[i], errs[i] = claimWithAdmission(t, stores[i],
				ctx, attempt, task.Revision, "claim-shared-key", strings.Repeat("c", 64),
			)
		}(i)
	}
	close(start)
	wg.Wait()

	createdCount := 0
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("shared-key claimer %d returned error = %v", i, errs[i])
		}
		if created[i] {
			createdCount++
		}
	}
	if createdCount > 1 {
		t.Fatalf("created count = %d, want at most one", createdCount)
	}
	if results[0].ID == "" || results[0].ID != results[1].ID {
		t.Fatalf("claimed attempts = %q and %q, want the same attempt ID", results[0].ID, results[1].ID)
	}
	attempts, err := first.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d err=%v, want exactly one", len(attempts), err)
	}
}

// Reusing one key for a different claim request is a conflict, not a replay.
func TestWorkflowClaimWithReusedKeyAndDifferentFingerprintConflicts(t *testing.T) {
	ctx := context.Background()
	first, second := openWorkflowStorePair(t)
	seedProject(t, first, "workflow-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-claim-key-conflict")
	taskID := domain.WorkflowTaskID("task-main")
	activateWorkflowForStoreTest(t, first, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, now, "key-conflict")
	task, _, err := first.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}

	stores := []*sqlite.Store{first, second}
	results := make([]domain.WorkflowAttempt, 2)
	created := make([]bool, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			claimAt := now.Add(time.Duration(2+i) * time.Second)
			attempt := workflowStoreAttempt(
				domain.WorkflowAttemptID("attempt-fp-"+string(rune('1'+i))),
				workflowID, taskID, "worker-"+string(rune('1'+i)), "lease-"+string(rune('1'+i)),
				claimAt, claimAt.Add(time.Minute),
			)
			results[i], created[i], errs[i] = claimWithAdmission(t, stores[i],
				ctx, attempt, task.Revision, "claim-shared-key",
				strings.Repeat(string(rune('d'+i)), 64),
			)
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i := range errs {
		if errs[i] == nil {
			if winner != -1 {
				t.Fatalf("both claims succeeded: %+v and %+v", results[0], results[1])
			}
			winner = i
			continue
		}
		if !errors.Is(errs[i], domain.ErrWorkflowIdempotencyConflict) {
			t.Fatalf("loser %d error = %v, want idempotency conflict", i, errs[i])
		}
	}
	if winner == -1 {
		t.Fatalf("no claim succeeded: errors=%v", errs)
	}
	if !created[winner] {
		t.Fatalf("winner reported created=false")
	}
	attempts, err := first.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("persisted attempts = %d err=%v, want exactly one", len(attempts), err)
	}
}

// Two settles of one attempt from two store instances must apply exactly once;
// the loser is an idempotent replay, not a second task-revision bump.
func TestWorkflowSettleIsSingleWinnerAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	first, second := openWorkflowStorePair(t)
	seedProject(t, first, "workflow-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-settle-race")
	taskID := domain.WorkflowTaskID("task-main")
	activateWorkflowForStoreTest(t, first, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, now, "settle-race")
	ready, _, err := first.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, created, err := claimWithAdmission(t, first,
		ctx, workflowStoreAttempt("attempt-race", workflowID, taskID, "worker-1", "lease-1",
			now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "settle-race-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}

	stores := []*sqlite.Store{first, second}
	applied := make([]bool, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, applied[i], errs[i] = stores[i].SettleWorkflowAttempt(
				sqlitetest.ExecutionContext(t, ctx, stores[i], claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateSucceeded, "",
				"settle-race-key-"+string(rune('1'+i)), strings.Repeat("d", 64), now.Add(3*time.Second),
			)
		}(i)
	}
	close(start)
	wg.Wait()

	appliedCount := 0
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("settle racer %d error = %v", i, errs[i])
		}
		if applied[i] {
			appliedCount++
		}
	}
	if appliedCount != 1 {
		t.Fatalf("applied count = %d, want exactly one", appliedCount)
	}
	task, _, err := first.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != domain.WorkflowTaskStateSucceeded {
		t.Fatalf("task state = %q, want succeeded", task.State)
	}
	if task.Revision != ready.Revision+2 {
		t.Fatalf("task revision = %d, want %d (one claim + one settle bump)", task.Revision, ready.Revision+2)
	}
}

// A blocked task must stay blocked even when a sibling task later succeeds: the
// ready-refresh sweep may only promote pending tasks.
func TestWorkflowSiblingSuccessCannotLaunderBlockedTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-blocked-sibling")
	stuckTask := domain.WorkflowTaskID("task-stuck")
	okTask := domain.WorkflowTaskID("task-ok")
	activateWorkflowForStoreTest(t, s, workflowID, []domain.WorkflowTask{
		workflowStoreTask(stuckTask, workflowID, 2, now),
		workflowStoreTask(okTask, workflowID, 1, now),
	}, now, "blocked-sibling")

	stuckReady, _, err := s.GetWorkflowTask(ctx, workflowID, stuckTask)
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(10 * time.Second)
	stuckClaim, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-stuck", workflowID, stuckTask, "worker-stuck", "lease-stuck",
			now.Add(2*time.Second), leaseUntil),
		stuckReady.Revision, "blocked-sibling-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim stuck task = %+v created=%v err=%v", stuckClaim, created, err)
	}
	expired, err := s.RecoverExpiredWorkflowAttempts(ctx, leaseUntil.Add(time.Second))
	if err != nil || expired != 1 {
		t.Fatalf("recovery = %d err=%v, want one expired attempt", expired, err)
	}
	blocked, found, err := s.GetWorkflowTask(ctx, workflowID, stuckTask)
	if err != nil || !found || blocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("stuck task = %+v found=%v err=%v, want blocked", blocked, found, err)
	}

	okReady, _, err := s.GetWorkflowTask(ctx, workflowID, okTask)
	if err != nil {
		t.Fatal(err)
	}
	okClaim, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-ok", workflowID, okTask, "worker-ok", "lease-ok",
			now.Add(12*time.Second), now.Add(2*time.Minute)),
		okReady.Revision, "blocked-sibling-ok-claim", strings.Repeat("d", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim sibling = %+v created=%v err=%v", okClaim, created, err)
	}
	if _, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, okClaim.ID, now.Add(13*time.Second)), workflowID, okClaim.ID, "lease-ok", domain.WorkflowAttemptStateSucceeded,
		"", "blocked-sibling-ok-settle", strings.Repeat("e", 64), now.Add(13*time.Second),
	); err != nil || !applied {
		t.Fatalf("settle sibling: applied=%v err=%v", applied, err)
	}

	stillBlocked, found, err := s.GetWorkflowTask(ctx, workflowID, stuckTask)
	if err != nil || !found || stillBlocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("stuck task after sibling success = %+v found=%v err=%v, want blocked", stillBlocked, found, err)
	}
	_, _, err = claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-stuck-retry", workflowID, stuckTask, "worker-2", "lease-2",
			now.Add(14*time.Second), now.Add(3*time.Minute)),
		stillBlocked.Revision, "blocked-sibling-reclaim", strings.Repeat("f", 64),
	)
	if !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("reclaim blocked task error = %v, want not claimable", err)
	}
}

// An idempotency key belongs to one operation; presenting a claim key to Settle
// or a settle key to Retry must conflict and leave state untouched.
func TestWorkflowIdempotencyKeyCannotBeReusedAcrossOperations(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-key-cross-op")
	taskID := domain.WorkflowTaskID("task-main")
	activateWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, now, "cross-op")
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-1", workflowID, taskID, "worker-1", "lease-1",
			now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "cross-op-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}

	_, _, err = s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(3*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateSucceeded, "",
		"cross-op-claim", strings.Repeat("d", 64), now.Add(3*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		t.Fatalf("settle with a claim key error = %v, want idempotency conflict", err)
	}
	unchanged, found, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || unchanged.State != domain.WorkflowAttemptStateClaimed {
		t.Fatalf("attempt after cross-operation settle = %+v found=%v err=%v", unchanged, found, err)
	}
	unchangedTask, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || unchangedTask.Revision != ready.Revision+1 {
		t.Fatalf("task after cross-operation settle = %+v err=%v", unchangedTask, err)
	}

	if _, applied, err := s.SettleWorkflowAttempt(
		sqlitetest.ExecutionContext(t, ctx, s, claimed.ID, now.Add(4*time.Second)), workflowID, claimed.ID, "lease-1", domain.WorkflowAttemptStateFailed,
		"boom", "cross-op-settle", strings.Repeat("e", 64), now.Add(4*time.Second),
	); err != nil || !applied {
		t.Fatalf("settle = applied=%v err=%v", applied, err)
	}
	retryWait, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if retryWait.State != domain.WorkflowTaskStateRetryWait {
		t.Fatalf("task state = %q, want retry_wait", retryWait.State)
	}
	_, _, err = s.RetryWorkflowTask(
		ctx, workflowID, taskID, retryWait.Revision, "cross-op-settle", strings.Repeat("f", 64),
		now.Add(5*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		t.Fatalf("retry with a settle key error = %v, want idempotency conflict", err)
	}
	stillWaiting, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || stillWaiting.State != domain.WorkflowTaskStateRetryWait || stillWaiting.Revision != retryWait.Revision {
		t.Fatalf("task after cross-operation retry = %+v err=%v, want unchanged retry_wait", stillWaiting, err)
	}
}

// recoveryBlockedAttempt drives one attempt through claim → lease expiry →
// recovery so the caller starts from the UNKNOWN/blocked trap. It returns the
// recovered attempt and the blocked task, both read back from storage.
func recoveryBlockedAttempt(
	t *testing.T,
	s *sqlite.Store,
	workflowID domain.WorkflowID,
	tasks []domain.WorkflowTask,
	keyPrefix string,
) (domain.WorkflowAttempt, domain.WorkflowTask, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := workflowStoreTime()
	activateWorkflowForStoreTest(t, s, workflowID, tasks, now, keyPrefix)

	taskID := tasks[0].ID
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatalf("load ready task: %v", err)
	}
	leaseUntil := now.Add(10 * time.Second)
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt(
			domain.WorkflowAttemptID("attempt-"+keyPrefix), workflowID, taskID, "worker-"+keyPrefix,
			"lease-"+keyPrefix, now.Add(2*time.Second), leaseUntil,
		),
		ready.Revision, keyPrefix+"-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim task = %+v created=%v err=%v", claimed, created, err)
	}
	if expired, err := s.RecoverExpiredWorkflowAttempts(ctx, leaseUntil.Add(time.Second)); err != nil || expired != 1 {
		t.Fatalf("recovery = %d err=%v, want one expired attempt", expired, err)
	}
	recovered, found, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || !found || recovered.State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("recovered attempt = %+v found=%v err=%v, want unknown", recovered, found, err)
	}
	blocked, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || blocked.State != domain.WorkflowTaskStateBlocked {
		t.Fatalf("recovered task = %+v found=%v err=%v, want blocked", blocked, found, err)
	}
	return recovered, blocked, leaseUntil.Add(2 * time.Second)
}

// Resolving an UNKNOWN attempt as succeeded must release its blocked task,
// clear the lease, and complete the workflow. The resolved task is terminal, so
// it cannot be claimed again, while a sibling ready task still can be.
func TestWorkflowResolveUnknownAttemptReleasesBlockedTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-resolve-succeeded")
	stuckTask := domain.WorkflowTaskID("task-stuck")
	siblingTask := domain.WorkflowTaskID("task-sibling")

	recovered, blocked, resolveAt := recoveryBlockedAttempt(t, s, workflowID, []domain.WorkflowTask{
		workflowStoreTask(stuckTask, workflowID, 3, now),
		workflowStoreTask(siblingTask, workflowID, 1, now),
	}, "resolve-succeeded")

	resolved, applied, err := s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateSucceeded, "",
		blocked.Revision, "resolve-succeeded-key", strings.Repeat("d", 64), resolveAt,
	)
	if err != nil || !applied {
		t.Fatalf("resolve = %+v applied=%v err=%v", resolved, applied, err)
	}
	if resolved.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("resolved attempt state = %q, want succeeded", resolved.State)
	}
	if resolved.LeaseToken != "" || resolved.LeaseUntil != nil {
		t.Fatalf("resolved attempt lease = %q/%v, want cleared", resolved.LeaseToken, resolved.LeaseUntil)
	}
	if resolved.EndedAt == nil {
		t.Fatal("resolved attempt has no ended_at")
	}
	if got := resolved.ErrorCode; got != "" {
		t.Fatalf("resolved attempt error code = %q, want empty", got)
	}

	succeeded, found, err := s.GetWorkflowTask(ctx, workflowID, stuckTask)
	if err != nil || !found || succeeded.State != domain.WorkflowTaskStateSucceeded {
		t.Fatalf("task after resolve = %+v found=%v err=%v, want succeeded", succeeded, found, err)
	}

	// The resolved task is terminal and its attempt left the active set, so no
	// further attempt may be launched for it.
	_, _, err = claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-after-resolve", workflowID, stuckTask, "worker-after",
			"lease-after", resolveAt.Add(time.Second), resolveAt.Add(time.Minute)),
		succeeded.Revision, "resolve-succeeded-reclaim", strings.Repeat("e", 64),
	)
	if !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("claim of resolved task error = %v, want not claimable", err)
	}

	// A sibling task that never expired is still schedulable.
	siblingReady, _, err := s.GetWorkflowTask(ctx, workflowID, siblingTask)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-sibling", workflowID, siblingTask, "worker-sibling",
			"lease-sibling", resolveAt.Add(2*time.Second), resolveAt.Add(time.Minute)),
		siblingReady.Revision, "resolve-succeeded-sibling", strings.Repeat("f", 64),
	); err != nil || !created {
		t.Fatalf("claim sibling task created=%v err=%v", created, err)
	}

	// The sibling is still running, so the workflow is not complete yet.
	runningWorkflow, _, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || runningWorkflow.State != domain.WorkflowStateActive {
		t.Fatalf("workflow with a live sibling = %+v err=%v, want active", runningWorkflow, err)
	}
}

// Resolving as failed while attempt budget remains puts the task in retry_wait,
// which is the state RetryWorkflowTask can re-open: proof the task really left
// the blocked trap instead of merely looking different.
func TestWorkflowResolveUnknownAttemptFailedEntersRetryWait(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-resolve-failed")
	taskID := domain.WorkflowTaskID("task-main")

	recovered, blocked, resolveAt := recoveryBlockedAttempt(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, "resolve-failed")

	resolved, applied, err := s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateFailed, "worker_lost",
		blocked.Revision, "resolve-failed-key", strings.Repeat("d", 64), resolveAt,
	)
	if err != nil || !applied {
		t.Fatalf("resolve = %+v applied=%v err=%v", resolved, applied, err)
	}
	if resolved.State != domain.WorkflowAttemptStateFailed || resolved.ErrorCode != "worker_lost" {
		t.Fatalf("resolved attempt = %+v, want failed with worker_lost", resolved)
	}
	retryWait, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || retryWait.State != domain.WorkflowTaskStateRetryWait {
		t.Fatalf("task after failed resolve = %+v found=%v err=%v, want retry_wait", retryWait, found, err)
	}
	ready, applied, err := s.RetryWorkflowTask(
		ctx, workflowID, taskID, retryWait.Revision, "resolve-failed-retry", strings.Repeat("e", 64), resolveAt.Add(time.Second),
	)
	if err != nil || !applied || ready.State != domain.WorkflowTaskStateReady {
		t.Fatalf("retry after resolve = %+v applied=%v err=%v, want ready", ready, applied, err)
	}
}

// A stale task revision is refused: the operator's CAS is what stops two
// reconcilers from applying conflicting outcomes to one expired attempt.
func TestWorkflowResolveRejectsStaleTaskRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-resolve-stale")
	taskID := domain.WorkflowTaskID("task-main")

	recovered, blocked, resolveAt := recoveryBlockedAttempt(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, "resolve-stale")

	_, _, err := s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateSucceeded, "",
		blocked.Revision-1, "resolve-stale-key", strings.Repeat("d", 64), resolveAt,
	)
	if !errors.Is(err, domain.ErrWorkflowRevisionConflict) {
		t.Fatalf("resolve with stale revision error = %v, want revision conflict", err)
	}
	unchangedAttempt, _, err := s.GetWorkflowAttempt(ctx, recovered.ID)
	if err != nil || unchangedAttempt.State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("attempt after stale resolve = %+v err=%v, want unknown", unchangedAttempt, err)
	}
	unchangedTask, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || unchangedTask.State != domain.WorkflowTaskStateBlocked ||
		unchangedTask.Revision != blocked.Revision {
		t.Fatalf("task after stale resolve = %+v err=%v, want unchanged blocked", unchangedTask, err)
	}
}

// Only an UNKNOWN attempt can be reconciled; a live claim is a worker's, not an
// operator's, and resolving it must be refused with no state change.
func TestWorkflowResolveRejectsNonUnknownAttempt(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-resolve-claimed")
	taskID := domain.WorkflowTaskID("task-main")
	activateWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, now, "resolve-claimed")
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, created, err := claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-claimed", workflowID, taskID, "worker-1", "lease-1",
			now.Add(2*time.Second), now.Add(time.Minute)),
		ready.Revision, "resolve-claimed-claim", strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("claim = %+v created=%v err=%v", claimed, created, err)
	}
	liveTask, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, claimed.ID, domain.WorkflowAttemptStateSucceeded, "",
		liveTask.Revision, "resolve-claimed-key", strings.Repeat("d", 64), now.Add(3*time.Second),
	)
	if !errors.Is(err, domain.ErrInvalidWorkflowTransition) {
		t.Fatalf("resolve of a claimed attempt error = %v, want invalid transition", err)
	}
	unchangedAttempt, _, err := s.GetWorkflowAttempt(ctx, claimed.ID)
	if err != nil || unchangedAttempt.State != domain.WorkflowAttemptStateClaimed ||
		unchangedAttempt.LeaseToken != "lease-1" {
		t.Fatalf("attempt after refused resolve = %+v err=%v, want untouched claim", unchangedAttempt, err)
	}
	unchangedTask, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || unchangedTask.State != domain.WorkflowTaskStateClaimed ||
		unchangedTask.Revision != liveTask.Revision {
		t.Fatalf("task after refused resolve = %+v err=%v, want untouched claim", unchangedTask, err)
	}
}

// Replaying a resolve with its original key is a no-op that reports the stored
// outcome; reusing that key with a different request is a conflict that leaves
// the first decided outcome in place.
func TestWorkflowResolveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-resolve-idempotent")
	taskID := domain.WorkflowTaskID("task-main")

	recovered, blocked, resolveAt := recoveryBlockedAttempt(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, "resolve-idempotent")

	first, applied, err := s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateFailed, "worker_lost",
		blocked.Revision, "resolve-idempotent-key", strings.Repeat("d", 64), resolveAt,
	)
	if err != nil || !applied {
		t.Fatalf("first resolve = %+v applied=%v err=%v", first, applied, err)
	}
	afterFirst, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}

	replay, applied, err := s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateFailed, "worker_lost",
		blocked.Revision, "resolve-idempotent-key", strings.Repeat("d", 64), resolveAt.Add(time.Second),
	)
	if err != nil || applied {
		t.Fatalf("replayed resolve = %+v applied=%v err=%v, want applied=false", replay, applied, err)
	}
	if replay.State != domain.WorkflowAttemptStateFailed || replay.ID != first.ID {
		t.Fatalf("replayed resolve = %+v, want the stored failed attempt", replay)
	}
	afterReplay, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || afterReplay.Revision != afterFirst.Revision || afterReplay.State != afterFirst.State {
		t.Fatalf("task after replay = %+v err=%v, want revision %d unchanged", afterReplay, err, afterFirst.Revision)
	}

	// Same key, different decided outcome and fingerprint: conflict, not overwrite.
	_, _, err = s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateSucceeded, "",
		afterFirst.Revision, "resolve-idempotent-key", strings.Repeat("e", 64), resolveAt.Add(2*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		t.Fatalf("resolve with a reused key error = %v, want idempotency conflict", err)
	}
	preserved, _, err := s.GetWorkflowAttempt(ctx, recovered.ID)
	if err != nil || preserved.State != domain.WorkflowAttemptStateFailed || preserved.ErrorCode != "worker_lost" {
		t.Fatalf("attempt after key conflict = %+v err=%v, want first outcome preserved", preserved, err)
	}
	preservedTask, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || preservedTask.Revision != afterFirst.Revision ||
		preservedTask.State != domain.WorkflowTaskStateRetryWait {
		t.Fatalf("task after key conflict = %+v err=%v, want unchanged retry_wait", preservedTask, err)
	}
}

// Resolving failed after the attempt budget is exhausted fails the task and the
// workflow, and nothing may then be dispatched: reconciliation decides outcomes,
// it never re-launches work.
func TestWorkflowResolveDoesNotRedispatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-resolve-no-redispatch")
	taskID := domain.WorkflowTaskID("task-main")

	recovered, blocked, resolveAt := recoveryBlockedAttempt(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, "no-redispatch")

	if _, applied, err := s.ResolveUnknownWorkflowAttempt(
		ctx, workflowID, recovered.ID, domain.WorkflowAttemptStateFailed, "worker_lost",
		blocked.Revision, "no-redispatch-key", strings.Repeat("d", 64), resolveAt,
	); err != nil || !applied {
		t.Fatalf("resolve = applied=%v err=%v", applied, err)
	}
	failedTask, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found || failedTask.State != domain.WorkflowTaskStateFailed {
		t.Fatalf("task after exhausted resolve = %+v found=%v err=%v, want failed", failedTask, found, err)
	}
	failedWorkflow, found, err := s.GetWorkflow(ctx, workflowID)
	if err != nil || !found || failedWorkflow.State != domain.WorkflowStateFailed {
		t.Fatalf("workflow after exhausted resolve = %+v found=%v err=%v, want failed", failedWorkflow, found, err)
	}
	_, _, err = claimWithAdmission(t, s,
		ctx, workflowStoreAttempt("attempt-redispatch", workflowID, taskID, "worker-2", "lease-2",
			resolveAt.Add(time.Second), resolveAt.Add(time.Minute)),
		failedTask.Revision, "no-redispatch-reclaim", strings.Repeat("e", 64),
	)
	if !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("claim after exhausted resolve error = %v, want not claimable", err)
	}
	attempts, err := s.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts after exhausted resolve = %d err=%v, want exactly one", len(attempts), err)
	}
}
