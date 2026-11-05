package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Binding test fixtures.
//
// Every binding test starts from a real workflow with a claimed attempt, because the
// binding's composite foreign key points at (id, workflow_id, task_id) on
// workflow_attempts. A test that skipped that would not exercise the constraint that
// makes cross-task binding impossible.

const bindingSpawnHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func bindingWorkflowRepo(workflowID domain.WorkflowID, projectID string) domain.WorkflowRepository {
	return domain.WorkflowRepository{
		WorkflowID: workflowID, RepoKey: "backend", ProjectID: domain.ProjectID(projectID),
		BaseSHA: "base-sha-1", TargetRef: "main",
	}
}

// seedClaimedAttempt creates a workflow with one repository and one claimed attempt,
// returning the attempt so a binding test can attach to it.
func seedClaimedAttempt(
	t *testing.T,
	s *sqlite.Store,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	projectID string,
	now time.Time,
) domain.WorkflowAttempt {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, projectID)

	task := workflowStoreTask(taskID, workflowID, 2, now)
	if _, _, err := s.CreateWorkflow(
		ctx, workflowStoreRecord(workflowID, now),
		[]domain.WorkflowRepository{bindingWorkflowRepo(workflowID, projectID)},
		[]domain.WorkflowTask{task}, nil, nil,
		"binding-create-"+string(workflowID), strings.Repeat("a", 64),
	); err != nil {
		t.Fatalf("CreateWorkflow(%s): %v", workflowID, err)
	}
	if _, _, err := s.ActivateWorkflow(ctx, workflowID, 1,
		"binding-activate-"+string(workflowID), strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatalf("ActivateWorkflow(%s): %v", workflowID, err)
	}
	ready, found, err := s.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil || !found {
		t.Fatalf("GetWorkflowTask(%s): found=%v err=%v", taskID, found, err)
	}
	claimed, created, err := s.ClaimWorkflowTask(
		ctx,
		workflowStoreAttempt(domain.WorkflowAttemptID("attempt-"+string(workflowID)), workflowID, taskID,
			"worker-1", "lease-secret-token", now.Add(2*time.Second), now.Add(time.Hour)),
		ready.Revision, "binding-claim-"+string(workflowID), strings.Repeat("c", 64),
	)
	if err != nil || !created {
		t.Fatalf("ClaimWorkflowTask(%s): created=%v err=%v", workflowID, created, err)
	}
	return claimed
}

// bindingIntent builds the intent a Bind call would persist.
func bindingIntent(
	attempt domain.WorkflowAttempt,
	projectID string,
) (domain.WorkflowAttemptBinding, []domain.WorkflowAttemptRepoBinding) {
	binding := domain.WorkflowAttemptBinding{
		AttemptID:      attempt.ID,
		WorkflowID:     attempt.WorkflowID,
		TaskID:         attempt.TaskID,
		SpawnRequestID: "spawn-" + string(attempt.ID),
		SpawnHash:      bindingSpawnHash,
	}
	repos := []domain.WorkflowAttemptRepoBinding{{
		RepoKey:         "backend",
		ProjectID:       domain.ProjectID(projectID),
		ExpectedBaseSHA: "base-sha-1",
		State:           domain.WorkflowRepoBindingStatePending,
	}}
	return binding, repos
}

// seedSession creates one AO session row so a binding can reference it.
func seedSession(t *testing.T, s *sqlite.Store, projectID string) domain.SessionID {
	t.Helper()
	rec := sampleRecord(projectID)
	created, err := s.CreateSession(context.Background(), rec)
	if err != nil {
		t.Fatalf("CreateSession(%s): %v", projectID, err)
	}
	return created.ID
}

func TestWorkflowBindingCreateIsIdempotentBySpawnRequest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-idem")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "bind-project", now)
	binding, repos := bindingIntent(attempt, "bind-project")

	first, created, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil || !created {
		t.Fatalf("first create = created:%v err:%v", created, err)
	}
	if first.State != domain.WorkflowBindingStateIntentPersisted {
		t.Fatalf("state = %q, want intent_persisted", first.State)
	}
	if first.Revision != 1 {
		t.Fatalf("revision = %d, want 1", first.Revision)
	}

	// Replaying the identical request must return the original and create nothing.
	second, created, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("replay err = %v", err)
	}
	if created {
		t.Fatal("replay reported a new binding; the spawn request id must be unique")
	}
	if second.AttemptID != first.AttemptID || second.SpawnRequestID != first.SpawnRequestID {
		t.Fatalf("replay returned a different binding: %+v vs %+v", second, first)
	}
	if second.Revision != first.Revision {
		t.Fatalf("replay bumped the revision: %d vs %d", second.Revision, first.Revision)
	}

	// Exactly one binding row and one repo row exist.
	stored, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	if stored.AttemptID != first.AttemptID {
		t.Fatalf("stored binding = %+v, want %+v", stored, first)
	}
	repoRows, err := s.ListWorkflowAttemptRepoBindings(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("list repo bindings: %v", err)
	}
	if len(repoRows) != 1 {
		t.Fatalf("repo bindings = %d, want 1 (a replay must not duplicate them)", len(repoRows))
	}
}

// TestWorkflowBindingIdempotentAcrossRestart proves the replay guarantee survives a
// daemon restart: the second store instance has no memory of the first.
func TestWorkflowBindingIdempotentAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-restart")

	first := sqlitetestOpenAt(t, dir)
	attempt := seedClaimedAttempt(t, first, workflowID, "task-a", "restart-project", now)
	binding, repos := bindingIntent(attempt, "restart-project")
	if _, created, err := first.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second)); err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second := sqlitetestReopen(t, dir)
	replayed, created, err := second.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("replay after restart err = %v", err)
	}
	if created {
		t.Fatal("a replay after restart created a second binding")
	}
	if replayed.SpawnRequestID != binding.SpawnRequestID {
		t.Fatalf("replay returned %q, want %q", replayed.SpawnRequestID, binding.SpawnRequestID)
	}
	// Reading by spawn request id is the restart-time lookup path.
	byRequest, found, err := second.GetWorkflowAttemptBindingBySpawnRequest(ctx, binding.SpawnRequestID)
	if err != nil || !found {
		t.Fatalf("lookup by spawn request after restart: found=%v err=%v", found, err)
	}
	if byRequest.AttemptID != attempt.ID {
		t.Fatalf("lookup returned attempt %q, want %q", byRequest.AttemptID, attempt.ID)
	}
}

// TestWorkflowBindingDifferentHashConflicts proves the fingerprint rule: the request
// id alone does not make two different requests the same request.
func TestWorkflowBindingDifferentHashConflicts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-hash")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "hash-project", now)
	binding, repos := bindingIntent(attempt, "hash-project")

	if _, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second)); err != nil {
		t.Fatalf("first create: %v", err)
	}

	conflicting := binding
	conflicting.SpawnHash = strings.Repeat("f", 64)
	_, created, err := s.CreateWorkflowAttemptBinding(ctx, conflicting, repos, now.Add(4*time.Second))
	if !errors.Is(err, domain.ErrWorkflowBindingSpawningRequestConflict) {
		t.Fatalf("conflicting hash err = %v, want ErrWorkflowBindingSpawningRequestConflict (created=%v)", err, created)
	}
}

// TestWorkflowBindingConcurrentCreateHasOneWinner proves the spawn request id is the
// single-winner fence for a racing Bind.
func TestWorkflowBindingConcurrentCreateHasOneWinner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-race-create")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "race-project", now)
	binding, repos := bindingIntent(attempt, "race-project")

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	results := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, created, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
			if err != nil {
				results[index] = err
				return
			}
			if created {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Fatalf("racer %d failed: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
}

// TestWorkflowBindingConcurrentBindHasOneWinner is the required concurrency test: N
// goroutines bind the same attempt to different sessions, and exactly one may win.
func TestWorkflowBindingConcurrentBindHasOneWinner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-race-bind")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "race-bind-project", now)
	binding, repos := bindingIntent(attempt, "race-bind-project")
	createdBinding, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	const racers = 8
	sessions := make([]domain.SessionID, racers)
	for i := range racers {
		sessions[i] = seedSession(t, s, "race-bind-project")
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	var winnerSession domain.SessionID
	failures := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, applied, err := s.BindWorkflowAttemptSession(
				ctx, attempt.ID, sessions[index], "gen-1",
				domain.WorkflowBindingStateBoundReady, createdBinding.Revision, now.Add(4*time.Second),
			)
			if err != nil {
				failures[index] = err
				return
			}
			if applied {
				mu.Lock()
				winners++
				winnerSession = sessions[index]
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (failures=%v)", winners, failures)
	}
	for i, err := range failures {
		if err == nil {
			continue
		}
		if !errors.Is(err, domain.ErrWorkflowBindingRevisionConflict) &&
			!errors.Is(err, domain.ErrWorkflowBindingSessionTaken) {
			t.Fatalf("racer %d failed with an unmapped error: %v", i, err)
		}
	}

	stored, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	if stored.SessionID == nil || *stored.SessionID != winnerSession {
		t.Fatalf("bound session = %v, want the winner %q", stored.SessionID, winnerSession)
	}
}

// TestWorkflowBindingSessionBelongsToOneAttempt proves the UNIQUE constraint and its
// stable error: a session bound to attempt A cannot be bound to attempt B.
func TestWorkflowBindingSessionBelongsToOneAttempt(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "shared-session-project"
	seedProject(t, s, projectID)

	workflowA := domain.WorkflowID("workflow-bind-session-a")
	attemptA := seedClaimedAttempt(t, s, workflowA, "task-a", projectID, now)
	workflowB := domain.WorkflowID("workflow-bind-session-b")
	attemptB := seedClaimedAttempt(t, s, workflowB, "task-b", projectID, now)

	bindingA, reposA := bindingIntent(attemptA, projectID)
	createdA, _, err := s.CreateWorkflowAttemptBinding(ctx, bindingA, reposA, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding A: %v", err)
	}
	bindingB, reposB := bindingIntent(attemptB, projectID)
	createdB, _, err := s.CreateWorkflowAttemptBinding(ctx, bindingB, reposB, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding B: %v", err)
	}

	session := seedSession(t, s, projectID)
	if _, applied, err := s.BindWorkflowAttemptSession(
		ctx, attemptA.ID, session, "gen-1",
		domain.WorkflowBindingStateBoundReady, createdA.Revision, now.Add(4*time.Second),
	); err != nil || !applied {
		t.Fatalf("bind A: applied=%v err=%v", applied, err)
	}

	_, applied, err := s.BindWorkflowAttemptSession(
		ctx, attemptB.ID, session, "gen-1",
		domain.WorkflowBindingStateBoundReady, createdB.Revision, now.Add(5*time.Second),
	)
	if err == nil {
		t.Fatal("binding the same session to a second attempt succeeded")
	}
	if !errors.Is(err, domain.ErrWorkflowBindingSessionTaken) {
		t.Fatalf("err = %v, want ErrWorkflowBindingSessionTaken (a raw SQLite string must not escape)", err)
	}
	if applied {
		t.Fatal("the losing bind reported applied")
	}
}

// TestWorkflowBindingWritesAttemptSessionInSameTransaction proves §4.1: the binding
// and the M1 attempt row carry the same session after a successful bind.
func TestWorkflowBindingWritesAttemptSessionInSameTransaction(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-mirror")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "mirror-project", now)
	binding, repos := bindingIntent(attempt, "mirror-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	session := seedSession(t, s, "mirror-project")
	bound, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "gen-7",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("bind: applied=%v err=%v", applied, err)
	}
	if bound.SessionID == nil || *bound.SessionID != session {
		t.Fatalf("binding session = %v, want %q", bound.SessionID, session)
	}
	if bound.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", bound.State)
	}
	if bound.ControllerGeneration != "gen-7" {
		t.Fatalf("generation = %q, want gen-7", bound.ControllerGeneration)
	}
	if bound.BoundAt == nil {
		t.Fatal("bound_at must be set when a session is bound")
	}

	storedAttempt, found, err := s.GetWorkflowAttempt(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get attempt: found=%v err=%v", found, err)
	}
	if storedAttempt.SessionID == nil || *storedAttempt.SessionID != session {
		t.Fatalf("attempt session = %v, want %q (the two truths must agree)", storedAttempt.SessionID, session)
	}
	// The bind must not disturb the lease: the attempt is still claimed by the worker.
	if storedAttempt.LeaseToken != attempt.LeaseToken {
		t.Fatal("binding changed the attempt's lease token")
	}
}

// TestWorkflowBindingRollbackLeavesNeitherSideWritten proves the same-transaction
// claim from the other direction: when the attempt mirror fails, the binding write is
// rolled back too.
func TestWorkflowBindingRollbackLeavesNeitherSideWritten(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-rollback")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "rollback-project", now)
	binding, repos := bindingIntent(attempt, "rollback-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// Pre-set the attempt's session so the mirror UPDATE matches zero rows. This is
	// the "attempt already has a session" case, and it must abort the whole
	// transaction rather than leave the binding pointing at a session the attempt
	// does not share.
	first := seedSession(t, s, "rollback-project")
	second := seedSession(t, s, "rollback-project")
	if _, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, first, "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	); err != nil || !applied {
		t.Fatalf("first bind: applied=%v err=%v", applied, err)
	}
	// Now the binding is bound_ready; a second bind must be refused by the state gate
	// before any SQL runs, leaving the first bind intact.
	after, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	_, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, second, "gen-2",
		domain.WorkflowBindingStateBoundReady, after.Revision, now.Add(5*time.Second),
	)
	if err == nil || applied {
		t.Fatalf("rebind: applied=%v err=%v, want a typed refusal", applied, err)
	}
	if !errors.Is(err, domain.ErrInvalidWorkflowBindingTransition) &&
		!errors.Is(err, domain.ErrWorkflowBindingSessionTaken) {
		t.Fatalf("rebind err = %v, want a typed binding refusal", err)
	}

	final, _, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if final.SessionID == nil || *final.SessionID != first {
		t.Fatalf("binding session = %v, want the original %q", final.SessionID, first)
	}
	storedAttempt, _, err := s.GetWorkflowAttempt(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if storedAttempt.SessionID == nil || *storedAttempt.SessionID != first {
		t.Fatalf("attempt session = %v, want %q", storedAttempt.SessionID, first)
	}
}

// TestWorkflowBindingRejectsIllegalTransition proves an out-of-graph transition is a
// typed domain error and leaves the row untouched.
func TestWorkflowBindingRejectsIllegalTransition(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-illegal")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "illegal-project", now)
	binding, repos := bindingIntent(attempt, "illegal-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// intent_persisted → stop_requested skips the entire spawn-and-bind path. It is
	// refused for the more specific reason: it is session-bearing and no session is held.
	_, applied, err := s.TransitionWorkflowAttemptBinding(
		ctx, attempt.ID, domain.WorkflowBindingStateStopRequested, "no", created.Revision, now.Add(4*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowBindingSessionRequired) {
		t.Fatalf("err = %v, want ErrWorkflowBindingSessionRequired", err)
	}
	if applied {
		t.Fatal("an illegal transition was applied")
	}

	unchanged, _, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if unchanged.State != domain.WorkflowBindingStateIntentPersisted {
		t.Fatalf("state = %q, want intent_persisted", unchanged.State)
	}
	if unchanged.Revision != created.Revision {
		t.Fatalf("revision = %d, want %d", unchanged.Revision, created.Revision)
	}
}

// TestWorkflowBindingTransitionRequiresRevision proves the CAS: a stale expected
// revision loses.
func TestWorkflowBindingTransitionRequiresRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-cas")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "cas-project", now)
	binding, repos := bindingIntent(attempt, "cas-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if _, applied, err := s.TransitionWorkflowAttemptBinding(
		ctx, attempt.ID, domain.WorkflowBindingStateSpawnRequested, "", created.Revision, now.Add(4*time.Second),
	); err != nil || !applied {
		t.Fatalf("transition: applied=%v err=%v", applied, err)
	}
	// Replaying with the now-stale revision must lose.
	_, applied, err := s.TransitionWorkflowAttemptBinding(
		ctx, attempt.ID, domain.WorkflowBindingStateSpawnUnknown, "spawn_lost", created.Revision, now.Add(5*time.Second),
	)
	if !errors.Is(err, domain.ErrWorkflowBindingRevisionConflict) {
		t.Fatalf("stale transition err = %v, want ErrWorkflowBindingRevisionConflict", err)
	}
	if applied {
		t.Fatal("a stale revision was applied")
	}
}

// TestWorkflowBindingRepoCrossWorkflowRejected proves the composite foreign key, not a
// store convention, rejects a repo_key the workflow never registered.
func TestWorkflowBindingRepoCrossWorkflowRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "cross-repo-project"
	workflowID := domain.WorkflowID("workflow-bind-cross-repo")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)

	binding, repos := bindingIntent(attempt, projectID)
	// "frontend" was never registered on this workflow; only "backend" was.
	repos[0].RepoKey = "frontend"
	_, created, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err == nil {
		t.Fatal("a repo binding for an unregistered repository was accepted")
	}
	if created {
		t.Fatal("the rejected create reported success")
	}
	if !errors.Is(err, domain.ErrWorkflowBindingRepoNotInWorkflow) {
		t.Fatalf("err = %v, want ErrWorkflowBindingRepoNotInWorkflow", err)
	}
	// The whole create must have rolled back, including the binding row.
	if _, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID); err != nil || found {
		t.Fatalf("binding survived a rejected repo insert: found=%v err=%v", found, err)
	}
}

// TestWorkflowBindingRepoPerRepoCAS proves each repository is verified independently
// and a drift is recorded rather than repaired.
func TestWorkflowBindingRepoPerRepoCAS(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "repo-cas-project"
	workflowID := domain.WorkflowID("workflow-bind-repo-cas")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	if _, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second)); err != nil {
		t.Fatalf("create binding: %v", err)
	}

	rows, err := s.ListWorkflowAttemptRepoBindings(ctx, attempt.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("repo bindings = %d err=%v, want 1", len(rows), err)
	}
	pending := rows[0]
	if pending.State != domain.WorkflowRepoBindingStatePending {
		t.Fatalf("state = %q, want pending", pending.State)
	}

	// Record a drift: the worktree head moved off the expected base.
	drifted := pending
	drifted.ObservedHeadSHA = "different-head-sha"
	drifted.State = domain.WorkflowRepoBindingStateMismatch
	recorded, applied, err := s.RecordWorkflowAttemptRepoBinding(ctx, drifted, pending.Revision, now.Add(4*time.Second))
	if err != nil || !applied {
		t.Fatalf("record: applied=%v err=%v", applied, err)
	}
	if recorded.State != domain.WorkflowRepoBindingStateMismatch {
		t.Fatalf("state = %q, want mismatch", recorded.State)
	}

	// Replaying with the now-stale revision loses.
	_, applied, err = s.RecordWorkflowAttemptRepoBinding(ctx, drifted, pending.Revision, now.Add(5*time.Second))
	if !errors.Is(err, domain.ErrWorkflowBindingRevisionConflict) {
		t.Fatalf("stale record err = %v, want ErrWorkflowBindingRevisionConflict", err)
	}
	if applied {
		t.Fatal("a stale repo revision was applied")
	}

	// The row still exists with its observation; nothing was deleted or reset.
	final, err := s.ListWorkflowAttemptRepoBindings(ctx, attempt.ID)
	if err != nil || len(final) != 1 {
		t.Fatalf("repo bindings after drift = %d err=%v, want 1", len(final), err)
	}
	if final[0].ObservedHeadSHA != "different-head-sha" {
		t.Fatalf("observed head = %q, want the drifted value preserved", final[0].ObservedHeadSHA)
	}
	if final[0].ExpectedBaseSHA != "base-sha-1" {
		t.Fatalf("expected base = %q, want the original expectation preserved", final[0].ExpectedBaseSHA)
	}
}

// TestWorkflowBindingNeverCarriesLeaseToken proves the new read shape exposes no lease
// capability, mirroring the M1 rule that exactly one response may return it.
func TestWorkflowBindingNeverCarriesLeaseToken(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-no-lease")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "no-lease-project", now)
	binding, repos := bindingIntent(attempt, "no-lease-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	session := seedSession(t, s, "no-lease-project")
	bound, _, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	// The lease is a real secret on this attempt; it must not appear anywhere in the
	// binding record's observable fields.
	if attempt.LeaseToken == "" {
		t.Fatal("fixture problem: the attempt has no lease token to leak")
	}
	rendered := bindingRenderable(bound)
	if strings.Contains(rendered, attempt.LeaseToken) {
		t.Fatalf("binding record leaked the lease token: %s", rendered)
	}
	if strings.Contains(rendered, "leaseToken") || strings.Contains(rendered, "LeaseToken") {
		t.Fatalf("binding record gained a lease field: %s", rendered)
	}
	// And the read path returns the same shape.
	read, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	if strings.Contains(bindingRenderable(read), attempt.LeaseToken) {
		t.Fatal("the read path leaked the lease token")
	}
}

// TestWorkflowBindingUnknownAttemptRejected proves the composite foreign key stops a
// binding from naming an attempt that does not exist.
func TestWorkflowBindingUnknownAttemptRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "unknown-attempt-project"
	seedProject(t, s, projectID)

	ghost := domain.WorkflowAttempt{
		ID: "attempt-ghost", WorkflowID: "workflow-ghost", TaskID: "task-ghost",
		WorkerID: "worker-1", LeaseToken: "token", LeaseUntil: ptrTime(now.Add(time.Hour)),
		State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now,
	}
	binding, repos := bindingIntent(ghost, projectID)
	if _, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(time.Second)); err == nil {
		t.Fatal("a binding for a nonexistent attempt was accepted")
	}
}

// TestWorkflowBindingValidationRejectsBadInput covers the argument checks that run
// before any SQL.
func TestWorkflowBindingValidationRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-validate")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "validate-project", now)
	binding, repos := bindingIntent(attempt, "validate-project")

	tests := []struct {
		name   string
		mutate func(*domain.WorkflowAttemptBinding)
		want   error
	}{
		{"short hash", func(b *domain.WorkflowAttemptBinding) { b.SpawnHash = "abc" }, domain.ErrInvalidWorkflowBinding},
		{"empty request id", func(b *domain.WorkflowAttemptBinding) { b.SpawnRequestID = "  " }, domain.ErrInvalidWorkflowBinding},
		{"empty attempt id", func(b *domain.WorkflowAttemptBinding) { b.AttemptID = "" }, domain.ErrInvalidWorkflowBinding},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := binding
			tt.mutate(&candidate)
			if _, _, err := s.CreateWorkflowAttemptBinding(ctx, candidate, repos, now.Add(time.Second)); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestWorkflowBindingBindRequiresGeneration proves the generation fence is enforced at
// the store boundary: a bind without a controller generation is refused.
func TestWorkflowBindingBindRequiresGeneration(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-gen")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "gen-project", now)
	binding, repos := bindingIntent(attempt, "gen-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	session := seedSession(t, s, "gen-project")

	if _, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "   ",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	); !errors.Is(err, domain.ErrInvalidWorkflowBindingSession) || applied {
		t.Fatalf("blank generation: applied=%v err=%v, want ErrInvalidWorkflowBindingSession", applied, err)
	}
	if _, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, "", "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	); !errors.Is(err, domain.ErrInvalidWorkflowBindingSession) || applied {
		t.Fatalf("blank session: applied=%v err=%v, want ErrInvalidWorkflowBindingSession", applied, err)
	}
}

// TestWorkflowBindingGetUnknownReportsNotFound proves a missing binding is a
// (zero, false, nil) read, not an error.
func TestWorkflowBindingGetUnknownReportsNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, found, err := s.GetWorkflowAttemptBinding(context.Background(), "attempt-missing"); err != nil || found {
		t.Fatalf("found=%v err=%v, want (false, nil)", found, err)
	}
	if _, found, err := s.GetWorkflowAttemptBindingBySpawnRequest(context.Background(), "spawn-missing"); err != nil || found {
		t.Fatalf("found=%v err=%v, want (false, nil)", found, err)
	}
}

// sqlitetestOpenAt opens a fresh isolated store at a caller-chosen directory.
func sqlitetestOpenAt(t *testing.T, dir string) *sqlite.Store {
	t.Helper()
	store, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatalf("open store at %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// sqlitetestReopen opens the database that a previous store already created, which
// is how a restart is simulated: no clone, no migration, just the existing file.
func sqlitetestReopen(t *testing.T, dir string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen store at %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ptrTime is a tiny helper for fixtures that need a *time.Time.
func ptrTime(value time.Time) *time.Time {
	return &value
}

// bindingRenderable renders every observable field of a binding so a test can assert
// the lease capability is absent from the whole shape rather than from one column.
func bindingRenderable(binding domain.WorkflowAttemptBinding) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "attempt=%s workflow=%s task=%s spawnRequest=%s spawnHash=%s state=%s ",
		binding.AttemptID, binding.WorkflowID, binding.TaskID, binding.SpawnRequestID,
		binding.SpawnHash, binding.State)
	if binding.SessionID != nil {
		fmt.Fprintf(&builder, "session=%s ", *binding.SessionID)
	}
	fmt.Fprintf(&builder, "generation=%s failure=%s revision=%d",
		binding.ControllerGeneration, binding.FailureCode, binding.Revision)
	return builder.String()
}

// TestWorkflowBindingSessionFKFailureIsNotBlamedOnTheRepo proves the constraint mapping
// is narrow. workflow_attempt_bindings.session_id is also a foreign key into sessions, so
// a bind naming a nonexistent session fails — and it must report the failure it actually
// had, not "this repository is not part of the workflow".
//
// This is a regression test: a broader mapping that matched any "FOREIGN KEY constraint
// failed" text reported the wrong cause here, which would have sent an operator looking
// for a repository problem while the real fault was a fabricated session id.
func TestWorkflowBindingSessionFKFailureIsNotBlamedOnTheRepo(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-bind-session-fk")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", "session-fk-project", now)
	binding, repos := bindingIntent(attempt, "session-fk-project")
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// No sessions row exists for this id.
	_, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, "session-that-does-not-exist", "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	)
	if err == nil {
		t.Fatal("binding a nonexistent session was accepted")
	}
	if applied {
		t.Fatal("the failed bind reported applied")
	}
	if errors.Is(err, domain.ErrWorkflowBindingRepoNotInWorkflow) {
		t.Fatalf("a session foreign-key failure was misreported as a repository problem: %v", err)
	}

	// The binding is untouched, so a correct session can still be bound.
	after, _, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if after.SessionID != nil || after.State != domain.WorkflowBindingStateIntentPersisted {
		t.Fatalf("state after a failed bind = %q session=%v, want intent_persisted with no session",
			after.State, after.SessionID)
	}
}

// TestWorkflowBindingSurvivesSessionDeletion is P0-1: deleting a terminated session must
// succeed, and the loss must be recorded as a durable fact rather than silently nulling
// the column.
//
// Before the fix this failed with a raw "CHECK constraint failed: state NOT IN
// ('bound_ready', ...)", which meant every M2 attempt that reached a session-bearing state
// permanently pinned its AO session and the user saw a SQLite string.
func TestWorkflowBindingSurvivesSessionDeletion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "delete-loss-project"
	workflowID := domain.WorkflowID("workflow-bind-delete")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// Condition 1 path: bind a session that the user then terminates and deletes.
	// DeleteTerminatedSession is the real user-facing path; it refuses a live session by
	// design, so the fixture terminates first.
	session := seedSession(t, s, projectID)
	rec, found, err := s.GetSession(ctx, session)
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate session: %v", err)
	}

	bound, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("bind: applied=%v err=%v", applied, err)
	}
	if bound.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", bound.State)
	}

	// The delete must SUCCEED. Before the fix it failed with a raw
	// "CHECK constraint failed", pinning every bound attempt's session forever.
	if _, err := s.DeleteTerminatedSession(ctx, session); err != nil {
		t.Fatalf("DeleteTerminatedSession failed, so a bound attempt pins its session forever: %v", err)
	}

	// Condition 2: the loss is a durable fact, not a silent null.
	after, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding after delete: found=%v err=%v", found, err)
	}
	if after.State != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required after the session was deleted", after.State)
	}
	if after.FailureCode != "session_deleted" {
		t.Fatalf("failure code = %q, want the stable session_deleted code", after.FailureCode)
	}
	if after.SessionID != nil {
		t.Fatalf("binding still names session %v after deletion", after.SessionID)
	}
	if after.BoundAt != nil {
		t.Fatal("binding still reports a bound_at after its session was deleted")
	}
	if after.Revision <= bound.Revision {
		t.Fatalf("revision = %d, want it advanced past %d so a reader sees a change", after.Revision, bound.Revision)
	}
	if after.Validate() != nil {
		t.Fatalf("the rewritten binding is invalid: %v", after.Validate())
	}
}

// TestWorkflowBindingNeverLooksLiveAfterSessionDeletion is P0-1's second acceptance
// condition stated as a property: no reader may see bound_ready or running once the
// session is gone, for every session-bearing starting state.
func TestWorkflowBindingNeverLooksLiveAfterSessionDeletion(t *testing.T) {
	ctx := context.Background()
	for _, startState := range []domain.WorkflowBindingState{
		domain.WorkflowBindingStateSessionProvisioning,
		domain.WorkflowBindingStateBoundReady,
		domain.WorkflowBindingStateRunning,
		domain.WorkflowBindingStateStopUnknown,
		domain.WorkflowBindingStateRecoveryUncertain,
	} {
		t.Run(string(startState), func(t *testing.T) {
			s := newTestStore(t)
			now := workflowStoreTime()
			projectID := "delete-live-project"
			workflowID := domain.WorkflowID("workflow-bind-live-" + string(startState))
			attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
			binding, repos := bindingIntent(attempt, projectID)
			created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
			if err != nil {
				t.Fatalf("create binding: %v", err)
			}

			session := seedSession(t, s, projectID)
			rec, found, err := s.GetSession(ctx, session)
			if err != nil || !found {
				t.Fatalf("get session: found=%v err=%v", found, err)
			}
			rec.IsTerminated = true
			if err := s.UpdateSession(ctx, rec); err != nil {
				t.Fatalf("terminate: %v", err)
			}

			// Bind at provisioning, then walk the legal graph to the state under test.
			bindTarget := domain.WorkflowBindingStateSessionProvisioning
			if startState == domain.WorkflowBindingStateBoundReady {
				bindTarget = domain.WorkflowBindingStateBoundReady
			}
			current, applied, err := s.BindWorkflowAttemptSession(
				ctx, attempt.ID, session, "gen-1",
				bindTarget, created.Revision, now.Add(4*time.Second),
			)
			if err != nil || !applied {
				t.Fatalf("bind: applied=%v err=%v", applied, err)
			}
			// bound_ready is one of the bind's own landing states, so it is reached by
			// binding rather than by a transition; the rest are walked through the graph.
			if startState != domain.WorkflowBindingStateSessionProvisioning &&
				startState != domain.WorkflowBindingStateBoundReady {
				// Every path starts by promoting the provisioning session to bound_ready,
				// which is the only edge out of session_provisioning that leads onward.
				path := []domain.WorkflowBindingState{domain.WorkflowBindingStateBoundReady}
				switch startState {
				case domain.WorkflowBindingStateRunning:
					path = append(path, domain.WorkflowBindingStateRunning)
				case domain.WorkflowBindingStateRecoveryUncertain:
					path = append(path, domain.WorkflowBindingStateRecoveryUncertain)
				case domain.WorkflowBindingStateStopUnknown:
					path = append(path,
						domain.WorkflowBindingStateRunning,
						domain.WorkflowBindingStateStopRequested,
						domain.WorkflowBindingStateStopUnknown,
					)
				}
				for _, next := range path {
					fromState := current.State
					current, applied, err = s.TransitionWorkflowAttemptBinding(
						ctx, attempt.ID, next, "", current.Revision, now.Add(5*time.Second),
					)
					if err != nil || !applied {
						t.Fatalf("transition from %q to %q: applied=%v err=%v",
							fromState, next, applied, err)
					}
				}
			}

			if _, err := s.DeleteTerminatedSession(ctx, session); err != nil {
				t.Fatalf("DeleteTerminatedSession from %q: %v", startState, err)
			}
			after, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
			if err != nil || !found {
				t.Fatalf("get binding: found=%v err=%v", found, err)
			}
			switch after.State {
			case domain.WorkflowBindingStateBoundReady, domain.WorkflowBindingStateRunning,
				domain.WorkflowBindingStateSessionProvisioning, domain.WorkflowBindingStateStopRequested,
				domain.WorkflowBindingStateStopConfirmed, domain.WorkflowBindingStateStopUnknown,
				domain.WorkflowBindingStateRecoveryUncertain:
				t.Fatalf("binding still reports the live state %q after its session was deleted", after.State)
			}
			if after.SessionID != nil {
				t.Fatalf("binding still names a session: %v", after.SessionID)
			}
		})
	}
}

// TestWorkflowBindingWalksSessionBearingStates is P0-2: the states that progress WITH a
// session must be reachable. Before the fix TransitionWorkflowAttemptBinding rejected
// every target with HasSession(), so running and the three stop states were dead code and
// M2-05's stop path could not be written at all.
func TestWorkflowBindingWalksSessionBearingStates(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "walk-project"
	workflowID := domain.WorkflowID("workflow-bind-walk")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	session := seedSession(t, s, projectID)
	current, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("bind: applied=%v err=%v", applied, err)
	}

	walk := func(next domain.WorkflowBindingState) domain.WorkflowAttemptBinding {
		t.Helper()
		updated, ok, err := s.TransitionWorkflowAttemptBinding(
			ctx, attempt.ID, next, "", current.Revision, now.Add(5*time.Second),
		)
		if err != nil || !ok {
			t.Fatalf("transition to %q: applied=%v err=%v", next, ok, err)
		}
		if updated.State != next {
			t.Fatalf("state = %q, want %q", updated.State, next)
		}
		if updated.SessionID == nil {
			t.Fatalf("state %q lost its session", next)
		}
		current = updated
		return updated
	}

	walk(domain.WorkflowBindingStateRunning)
	walk(domain.WorkflowBindingStateStopRequested)
	walk(domain.WorkflowBindingStateStopConfirmed)
}

// TestWorkflowBindingWalksStopUnknown proves the second stop outcome is reachable too,
// because a lost stop receipt is the case M2-05 must be able to record.
func TestWorkflowBindingWalksStopUnknown(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "walk-unknown-project"
	workflowID := domain.WorkflowID("workflow-bind-walk-unknown")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	session := seedSession(t, s, projectID)
	current, _, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "gen-1",
		domain.WorkflowBindingStateBoundReady, created.Revision, now.Add(4*time.Second),
	)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	for _, next := range []domain.WorkflowBindingState{
		domain.WorkflowBindingStateRunning,
		domain.WorkflowBindingStateStopRequested,
		domain.WorkflowBindingStateStopUnknown,
	} {
		updated, applied, err := s.TransitionWorkflowAttemptBinding(
			ctx, attempt.ID, next, "", current.Revision, now.Add(5*time.Second),
		)
		if err != nil || !applied {
			t.Fatalf("transition to %q: applied=%v err=%v", next, applied, err)
		}
		current = updated
	}
	if current.SessionID == nil || *current.SessionID != session {
		t.Fatal("the stop_unknown binding lost the session it could not stop")
	}
}

// TestWorkflowBindingTransitionCannotAcquireSession is P0-2's required negative test: the
// state-update path must never be how a binding gains a session. Only the bind CAS does
// that, because only it mirrors the session onto workflow_attempts.
func TestWorkflowBindingTransitionCannotAcquireSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "no-acquire-project"
	workflowID := domain.WorkflowID("workflow-bind-no-acquire")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	// intent_persisted holds no session.
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// Every session-bearing target must be refused, whether the graph would allow it
	// (session_provisioning, bound_ready) or not (running, the stop states): the reason
	// here is specifically that no session is held yet.
	for _, next := range []domain.WorkflowBindingState{
		domain.WorkflowBindingStateSessionProvisioning,
		domain.WorkflowBindingStateBoundReady,
		domain.WorkflowBindingStateRunning,
		domain.WorkflowBindingStateStopRequested,
		domain.WorkflowBindingStateRecoveryUncertain,
	} {
		_, applied, err := s.TransitionWorkflowAttemptBinding(
			ctx, attempt.ID, next, "", created.Revision, now.Add(4*time.Second),
		)
		if !errors.Is(err, domain.ErrWorkflowBindingSessionRequired) {
			t.Fatalf("transition to %q err = %v, want ErrWorkflowBindingSessionRequired", next, err)
		}
		if applied {
			t.Fatalf("the state-update path acquired a session by moving to %q", next)
		}
	}

	unchanged, _, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if unchanged.State != domain.WorkflowBindingStateIntentPersisted || unchanged.SessionID != nil {
		t.Fatalf("binding changed: state=%q session=%v", unchanged.State, unchanged.SessionID)
	}
}

// TestWorkflowBindingBindRejectsRunningTarget proves the bind CAS cannot be used to claim
// a provider started: running requires runtime evidence, not a session row.
func TestWorkflowBindingBindRejectsRunningTarget(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "no-running-project"
	workflowID := domain.WorkflowID("workflow-bind-no-running")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	session := seedSession(t, s, projectID)

	for _, target := range []domain.WorkflowBindingState{
		domain.WorkflowBindingStateRunning,
		domain.WorkflowBindingStateStopRequested,
		domain.WorkflowBindingStateRecoveryUncertain,
		domain.WorkflowBindingStateFailedConfirmed,
	} {
		_, applied, err := s.BindWorkflowAttemptSession(
			ctx, attempt.ID, session, "gen-1", target, created.Revision, now.Add(4*time.Second),
		)
		if !errors.Is(err, domain.ErrInvalidWorkflowBindingTransition) {
			t.Fatalf("bind to %q err = %v, want ErrInvalidWorkflowBindingTransition", target, err)
		}
		if applied {
			t.Fatalf("bind landed on %q", target)
		}
	}
}

// TestWorkflowBindingProvisioningIsSessionBearing is the P2 fix: session_provisioning used
// to claim a session in its name while forbidding one in the schema.
func TestWorkflowBindingProvisioningIsSessionBearing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "provisioning-project"
	workflowID := domain.WorkflowID("workflow-bind-provisioning")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-a", projectID, now)
	binding, repos := bindingIntent(attempt, projectID)
	created, _, err := s.CreateWorkflowAttemptBinding(ctx, binding, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	session := seedSession(t, s, projectID)
	provisioning, applied, err := s.BindWorkflowAttemptSession(
		ctx, attempt.ID, session, "gen-1",
		domain.WorkflowBindingStateSessionProvisioning, created.Revision, now.Add(4*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("bind to provisioning: applied=%v err=%v", applied, err)
	}
	if provisioning.SessionID == nil || *provisioning.SessionID != session {
		t.Fatalf("provisioning binding session = %v, want %q", provisioning.SessionID, session)
	}
	if provisioning.State == domain.WorkflowBindingStateRunning {
		t.Fatal("a provisioning session reported running")
	}
	// And it is not a dead end: the promotion to bound_ready is reachable through the
	// graph, because the binding already holds its session. Acquiring and progressing are
	// separate operations by design.
	promoted, applied, err := s.TransitionWorkflowAttemptBinding(
		ctx, attempt.ID, domain.WorkflowBindingStateBoundReady, "", provisioning.Revision, now.Add(5*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("promote: applied=%v err=%v", applied, err)
	}
	if promoted.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", promoted.State)
	}
	if promoted.SessionID == nil {
		t.Fatal("promotion lost the session")
	}
}
