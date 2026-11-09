package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// rawWorkflowDatabase opens a second connection to the store's file so a test
// can drive statements the store API never issues (a no-op UPDATE that must not
// emit) and read change_log columns the cdc.Event type flattens to "".
func rawWorkflowDatabase(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+failureStorePragmas)
	if err != nil {
		t.Fatalf("open raw workflow database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

// allEventsAfter reads every change_log event above seq. The workflow tests
// assert on the full stream so an unexpected extra event fails loudly.
func allEventsAfter(t *testing.T, s *sqlite.Store, after int64) []cdc.Event {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), after, 1000)
	if err != nil {
		t.Fatalf("read change_log after %d: %v", after, err)
	}
	return events
}

func latestSeq(t *testing.T, s *sqlite.Store) int64 {
	t.Helper()
	seq, err := s.LatestSeq(context.Background())
	if err != nil {
		t.Fatalf("read change_log head: %v", err)
	}
	return seq
}

func workflowEventOfType(t *testing.T, events []cdc.Event, eventType cdc.EventType) cdc.Event {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("no %s event in %v", eventType, workflowEventTypeSeqs(events))
	return cdc.Event{}
}

func workflowEventTypeSeqs(events []cdc.Event) []string {
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, string(event.Type))
	}
	return got
}

func countWorkflowEvents(t *testing.T, events []cdc.Event) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, event := range events {
		switch event.Type {
		case cdc.EventWorkflowCreated, cdc.EventWorkflowUpdated,
			cdc.EventWorkflowTaskUpdated, cdc.EventWorkflowAttemptUpdated:
			counts[string(event.Type)]++
		}
	}
	return counts
}

// TestWorkflowCDCCreateEmitsOneWorkflowEventPerTask pins the create-time event
// volume: the insert trigger fires once for the workflow and once per task, and
// an idempotent replay adds nothing.
func TestWorkflowCDCCreateEmitsOneWorkflowEventPerTask(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "workflow-cdc-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-cdc-create")
	tasks := []domain.WorkflowTask{
		workflowStoreTask("task-cdc-a", workflowID, 2, now),
		workflowStoreTask("task-cdc-b", workflowID, 1, now),
	}
	key, fingerprint := "workflow-cdc-create", strings.Repeat("a", 64)
	createWorkflowForStoreTest(t, s, workflowID, tasks, nil, key, fingerprint, now)

	counts := countWorkflowEvents(t, allEventsAfter(t, s, 0))
	if counts["workflow_created"] != 1 {
		t.Errorf("workflow_created events = %d, want 1", counts["workflow_created"])
	}
	if counts["workflow_task_updated"] != len(tasks) {
		t.Errorf("workflow_task_updated events = %d, want %d", counts["workflow_task_updated"], len(tasks))
	}
	if counts["workflow_updated"] != 0 || counts["workflow_attempt_updated"] != 0 {
		t.Errorf("create emitted update events: %v", counts)
	}

	created := workflowEventOfType(t, allEventsAfter(t, s, 0), cdc.EventWorkflowCreated)
	for _, snippet := range []string{
		`"kind":"workflow"`, `"workflowId":"` + string(workflowID) + `"`,
		`"state":"draft"`, `"revision":1`,
	} {
		if !strings.Contains(string(created.Payload), snippet) {
			t.Errorf("workflow_created payload %s is missing %s", created.Payload, snippet)
		}
	}

	// The replay reuses the idempotency key; no row is written, so no event is.
	baseline := latestSeq(t, s)
	if _, inserted := createWorkflowForStoreTest(t, s, "workflow-cdc-create-replay", nil, nil, key, fingerprint, now); inserted {
		t.Fatal("idempotent replay inserted a second workflow")
	}
	if events := allEventsAfter(t, s, baseline); len(events) != 0 {
		t.Fatalf("idempotent replay emitted %d CDC events, want 0: %v", len(events), events)
	}
}

// TestWorkflowCDCActivateEmitsWorkflowAndPromotedTaskEvents checks activation:
// the workflow state/revision change is one event, and each task promoted from
// pending to ready is one task event.
func TestWorkflowCDCActivateEmitsWorkflowAndPromotedTaskEvents(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "workflow-cdc-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-cdc-activate")
	tasks := []domain.WorkflowTask{
		workflowStoreTask("task-cdc-a", workflowID, 1, now),
		workflowStoreTask("task-cdc-b", workflowID, 1, now),
	}
	createWorkflowForStoreTest(t, s, workflowID, tasks, nil,
		"workflow-cdc-activate", strings.Repeat("b", 64), now)

	baseline := latestSeq(t, s)
	if _, applied, err := s.ActivateWorkflow(
		ctx, workflowID, 1, "workflow-cdc-activate-key", strings.Repeat("c", 64), now.Add(time.Second),
	); err != nil || !applied {
		t.Fatalf("ActivateWorkflow applied=%v err=%v", applied, err)
	}

	events := allEventsAfter(t, s, baseline)
	counts := countWorkflowEvents(t, events)
	if counts["workflow_updated"] != 1 {
		t.Errorf("workflow_updated events = %d, want 1 (all=%v)", counts["workflow_updated"], counts)
	}
	if counts["workflow_task_updated"] != len(tasks) {
		t.Errorf("workflow_task_updated events = %d, want %d promoted tasks", counts["workflow_task_updated"], len(tasks))
	}
	if len(counts) != 2 {
		t.Errorf("activation emitted unexpected workflow event types: %v", counts)
	}

	updated := workflowEventOfType(t, events, cdc.EventWorkflowUpdated)
	for _, snippet := range []string{
		`"workflowId":"` + string(workflowID) + `"`, `"state":"active"`, `"revision":2`,
	} {
		if !strings.Contains(string(updated.Payload), snippet) {
			t.Errorf("workflow_updated payload %s is missing %s", updated.Payload, snippet)
		}
	}
	promoted := 0
	for _, event := range events {
		if event.Type == cdc.EventWorkflowTaskUpdated && strings.Contains(string(event.Payload), `"state":"ready"`) {
			promoted++
		}
	}
	if promoted != len(tasks) {
		t.Errorf("workflow_task_updated events carrying ready = %d, want %d", promoted, len(tasks))
	}
}

// TestWorkflowCDCNoOpTaskAndWorkflowUpdatesEmitNothing is the WHEN-guard proof:
// an UPDATE that changes neither state nor revision must not reach change_log,
// and neither must a lease/heartbeat rewrite that keeps the attempt's state.
func TestWorkflowCDCNoOpTaskAndWorkflowUpdatesEmitNothing(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	seedProject(t, s, "workflow-cdc-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-cdc-noop")
	taskID := domain.WorkflowTaskID("task-cdc-noop")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 2, now)}, nil,
		"workflow-cdc-noop", strings.Repeat("d", 64), now)
	if _, applied, err := s.ActivateWorkflow(
		ctx, workflowID, 1, "workflow-cdc-noop-activate", strings.Repeat("e", 64), now.Add(time.Second),
	); err != nil || !applied {
		t.Fatalf("ActivateWorkflow applied=%v err=%v", applied, err)
	}
	ready, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowTask found=%v err=%v", found, err)
	}
	claim := workflowStoreAttempt("attempt-cdc-noop", workflowID, taskID, "worker-noop", "lease-noop",
		now.Add(2*time.Second), now.Add(2*time.Minute))
	if _, created, err := claimWithAdmission(ctx, t, s, claim, ready.Revision, "workflow-cdc-noop-claim", strings.Repeat("f", 64)); err != nil || !created {
		t.Fatalf("ClaimWorkflowTask created=%v err=%v", created, err)
	}

	raw := rawWorkflowDatabase(t, dataDir)
	baseline := latestSeq(t, s)
	for _, statement := range []string{
		`UPDATE workflow_tasks SET priority = priority + 1 WHERE workflow_id = 'workflow-cdc-noop'`,
		`UPDATE workflow_tasks SET brief = 'rewritten brief' WHERE workflow_id = 'workflow-cdc-noop'`,
		`UPDATE workflows SET title = 'renamed workflow' WHERE id = 'workflow-cdc-noop'`,
		`UPDATE workflows SET goal = 'rewritten goal' WHERE id = 'workflow-cdc-noop'`,
		`UPDATE workflow_attempts SET heartbeat_at = datetime('now'), updated_at = datetime('now') WHERE workflow_id = 'workflow-cdc-noop'`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("no-op statement %q: %v", statement, err)
		}
	}

	if events := allEventsAfter(t, s, baseline); len(events) != 0 {
		t.Fatalf("non-state updates emitted %d CDC events, want 0: %v", len(events), events)
	}

	// The task guard still fires on a real revision change.
	if _, err := raw.Exec(`UPDATE workflow_tasks SET revision = revision + 1 WHERE workflow_id = 'workflow-cdc-noop'`); err != nil {
		t.Fatalf("revision update: %v", err)
	}
	if events := allEventsAfter(t, s, baseline); len(events) != 1 ||
		events[0].Type != cdc.EventWorkflowTaskUpdated {
		t.Fatalf("revision update emitted %v, want one workflow_task_updated", events)
	}
}

// TestWorkflowCDCClaimEmitsAttemptEventWithoutLeaseCapability asserts the claim
// event carries the attempt identity and state but never the lease token or
// worker id, which are capabilities rather than observable state.
func TestWorkflowCDCClaimEmitsAttemptEventWithoutLeaseCapability(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "workflow-cdc-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-cdc-claim")
	taskID := domain.WorkflowTaskID("task-cdc-claim")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"workflow-cdc-claim", strings.Repeat("g", 64), now)
	if _, applied, err := s.ActivateWorkflow(
		ctx, workflowID, 1, "workflow-cdc-claim-activate", strings.Repeat("h", 64), now.Add(time.Second),
	); err != nil || !applied {
		t.Fatalf("ActivateWorkflow applied=%v err=%v", applied, err)
	}
	ready, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowTask found=%v err=%v", found, err)
	}

	const workerID, leaseToken = "worker-cdc-claim", "lease-cdc-claim-secret"
	attempt := workflowStoreAttempt("attempt-cdc-claim", workflowID, taskID, workerID, leaseToken,
		now.Add(2*time.Second), now.Add(2*time.Minute))
	baseline := latestSeq(t, s)
	claimed, created, err := claimWithAdmission(ctx, t, s, attempt, ready.Revision, "workflow-cdc-claim-key", strings.Repeat("i", 64))
	if err != nil || !created {
		t.Fatalf("ClaimWorkflowTask created=%v err=%v", created, err)
	}

	events := allEventsAfter(t, s, baseline)
	var attempts []cdc.Event
	for _, event := range events {
		if event.Type == cdc.EventWorkflowAttemptUpdated {
			attempts = append(attempts, event)
		}
	}
	if len(attempts) == 0 {
		t.Fatalf("claim emitted no workflow_attempt_updated event (all=%v)", events)
	}
	for _, event := range attempts {
		payload := string(event.Payload)
		if strings.Contains(payload, leaseToken) {
			t.Fatalf("workflow_attempt_updated payload leaked the lease token: %s", payload)
		}
		if strings.Contains(payload, workerID) {
			t.Fatalf("workflow_attempt_updated payload leaked the worker id: %s", payload)
		}
		for _, snippet := range []string{
			`"kind":"workflow_attempt"`, `"attemptId":"` + string(claimed.ID) + `"`,
			`"attemptNo":1`, `"state":"claimed"`,
		} {
			if !strings.Contains(payload, snippet) {
				t.Errorf("workflow_attempt_updated payload %s is missing %s", payload, snippet)
			}
		}
	}
	// The task transition ready -> claimed is a real state change and must show.
	if counts := countWorkflowEvents(t, events); counts["workflow_task_updated"] == 0 {
		t.Errorf("claim emitted no workflow_task_updated event: %v", counts)
	}
}

// TestWorkflowCDCEventsCarryNoProjectOrSessionScope pins the design decision:
// a workflow spans several projects, so its events are unscoped and the
// identifiers live in the payload.
func TestWorkflowCDCEventsCarryNoProjectOrSessionScope(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	seedProject(t, s, "workflow-cdc-project")
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-cdc-scope")
	taskID := domain.WorkflowTaskID("task-cdc-scope")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"workflow-cdc-scope", strings.Repeat("j", 64), now)
	if _, applied, err := s.ActivateWorkflow(
		ctx, workflowID, 1, "workflow-cdc-scope-activate", strings.Repeat("k", 64), now.Add(time.Second),
	); err != nil || !applied {
		t.Fatalf("ActivateWorkflow applied=%v err=%v", applied, err)
	}
	ready, _, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := claimWithAdmission(ctx, t, s,
		workflowStoreAttempt("attempt-cdc-scope", workflowID, taskID, "worker-scope", "lease-scope",
			now.Add(2*time.Second), now.Add(2*time.Minute)),
		ready.Revision, "workflow-cdc-scope-claim", strings.Repeat("l", 64),
	); err != nil || !created {
		t.Fatalf("ClaimWorkflowTask created=%v err=%v", created, err)
	}

	for _, event := range allEventsAfter(t, s, 0) {
		if event.ProjectID != "" || event.SessionID != "" {
			t.Errorf("%s event carried project=%q session=%q, want both empty",
				event.Type, event.ProjectID, event.SessionID)
		}
	}

	raw := rawWorkflowDatabase(t, dataDir)
	var scoped int
	if err := raw.QueryRow(`
SELECT COUNT(*) FROM change_log
WHERE event_type IN ('workflow_created', 'workflow_updated', 'workflow_task_updated', 'workflow_attempt_updated')
  AND (project_id IS NOT NULL OR session_id IS NOT NULL)`).Scan(&scoped); err != nil {
		t.Fatalf("count scoped workflow events: %v", err)
	}
	if scoped != 0 {
		t.Fatalf("%d workflow change_log rows carry a project_id or session_id, want 0", scoped)
	}
	var unscoped int
	if err := raw.QueryRow(`
SELECT COUNT(*) FROM change_log
WHERE event_type IN ('workflow_created', 'workflow_updated', 'workflow_task_updated', 'workflow_attempt_updated')
  AND project_id IS NULL AND session_id IS NULL`).Scan(&unscoped); err != nil {
		t.Fatalf("count unscoped workflow events: %v", err)
	}
	if unscoped == 0 {
		t.Fatal("no workflow change_log rows were emitted at all")
	}
}

// TestWorkflowRollbackEmitsNoCDCEvent proves the change_log triggers fire inside
// the creating transaction, not after it commits: a create that fails on a
// foreign key rolls the whole transaction back, so the workflow row, its tasks,
// and any trigger rows they produced must all disappear together. Without that,
// a failed write would announce a workflow to every connected board that no
// caller can ever read back.
func TestWorkflowRollbackEmitsNoCDCEvent(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	seedProject(t, s, "workflow-cdc-project")
	now := workflowStoreTime()

	workflowID := domain.WorkflowID("workflow-cdc-rollback")
	task := workflowStoreTask("task-cdc-rollback", workflowID, 1, now)
	// The task binds to a repository key the workflow never declares, so the
	// composite foreign key on workflow_task_repos rejects the insert after the
	// workflow and task rows have already been written inside the transaction.
	orphanBinding := domain.WorkflowTaskRepository{
		WorkflowID: workflowID, TaskID: task.ID, RepoKey: "no-such-repo",
		AllowedPaths: []string{"src"},
	}
	raw := rawWorkflowDatabase(t, dataDir)
	workflowCDCRows := func() int {
		t.Helper()
		var count int
		if err := raw.QueryRow(
			`SELECT COUNT(*) FROM change_log WHERE event_type LIKE 'workflow%'`,
		).Scan(&count); err != nil {
			t.Fatalf("count workflow change_log rows: %v", err)
		}
		return count
	}

	baseline := workflowCDCRows()
	if _, _, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(workflowID, now), nil, []domain.WorkflowTask{task},
		nil, []domain.WorkflowTaskRepository{orphanBinding},
		"workflow-cdc-rollback-key", strings.Repeat("m", 64),
	); err == nil {
		t.Fatal("CreateWorkflow accepted a task binding to an unknown repository")
	}
	if got := workflowCDCRows(); got != baseline {
		t.Fatalf("rolled-back create emitted %d workflow change_log rows, want 0 above baseline %d", got-baseline, baseline)
	}

	// The same query must see a successful create, or the assertion above would
	// pass for a query that never matches anything.
	if _, inserted := createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{task}, nil,
		"workflow-cdc-rollback-ok", strings.Repeat("n", 64), now,
	); !inserted {
		t.Fatal("the retry after the rolled-back transaction did not insert")
	}
	if got := workflowCDCRows() - baseline; got == 0 {
		t.Fatal("a successful create emitted no workflow change_log rows; the count query is wrong")
	}
}
