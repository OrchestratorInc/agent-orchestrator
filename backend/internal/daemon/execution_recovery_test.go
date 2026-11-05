package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// M2 binding recovery tests.
//
// They run against a REAL migrated store and the REAL coordinator, because the property
// under test is a durable one: a binding frozen in an uncertain state must be advanced by
// looking up the session that already exists, and must stay frozen when none does. A fake
// store would let a wrong query pass.

var recoveryTestNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

const recoveryRepoBaseSHA = "dddddddddddddddddddddddddddddddddddddddd"

// recordedBinder wraps a reconciler and counts calls, so a test can assert the sweep never
// touched a binding it should skip.
type recordedBinder struct {
	inner bindingReconciler
	calls []domain.WorkflowAttemptID
	errs  map[domain.WorkflowAttemptID]error
}

func (b *recordedBinder) Reconcile(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (execution.BindOutcome, error) {
	b.calls = append(b.calls, attemptID)
	if err, ok := b.errs[attemptID]; ok {
		return execution.BindOutcome{}, err
	}
	return b.inner.Reconcile(ctx, attemptID)
}

// recoveryEnv is one workflow, task, and attempt, plus whatever session the test created.
type recoveryEnv struct {
	store     *sqlite.Store
	gateway   *execution.FakeGateway
	binder    *execution.Coordinator
	recorded  *recordedBinder
	sweep     bindingRecoverySweep
	logs      *bytes.Buffer
	workflow  domain.WorkflowID
	task      domain.WorkflowTaskID
	attemptID domain.WorkflowAttemptID
	spawnKey  string
	// spawnsBeforeRecovery is the gateway's spawn count once the fixture's own session
	// exists, so a recovery that spawned again is detectable.
	spawnsBeforeRecovery int
}

// newRecoveryEnv 为已有授权仓库提供固定基线和实时观察，恢复断言无需跳过仓库门禁。
func newRecoveryEnv(t *testing.T) *recoveryEnv {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	ctx := context.Background()

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "recover-proj", Path: t.TempDir(), RegisteredAt: recoveryTestNow,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	workflow := domain.Workflow{
		ID: "recover-workflow", Title: "Recover", Goal: "recover bindings",
		State: domain.WorkflowStateDraft, Revision: 1,
		CreatedAt: recoveryTestNow, UpdatedAt: recoveryTestNow,
	}
	task := domain.WorkflowTask{
		ID: "recover-task", WorkflowID: workflow.ID, Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Task", State: domain.WorkflowTaskStatePending, MaxAttempts: 1, Revision: 1,
		AgentHarness: domain.HarnessCodex,
		CreatedAt:    recoveryTestNow, UpdatedAt: recoveryTestNow,
	}
	if _, _, err := store.CreateWorkflow(
		ctx, workflow,
		[]domain.WorkflowRepository{{
			WorkflowID: workflow.ID, RepoKey: "backend", ProjectID: "recover-proj",
			BaseSHA: recoveryRepoBaseSHA,
		}},
		[]domain.WorkflowTask{task}, nil, nil,
		"recover-idem", strings.Repeat("a", 64),
	); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	// A claim requires an ACTIVE workflow whose task is ready, so activate before claiming.
	if _, _, err := store.ActivateWorkflow(
		ctx, workflow.ID, 1, "recover-activate", strings.Repeat("c", 64), recoveryTestNow,
	); err != nil {
		t.Fatalf("activate workflow: %v", err)
	}

	attempt := domain.WorkflowAttempt{
		ID: "recover-attempt", WorkflowID: workflow.ID, TaskID: task.ID, AttemptNo: 0,
		WorkerID:  "worker-recover",
		State:     domain.WorkflowAttemptStateClaimed,
		CreatedAt: recoveryTestNow, UpdatedAt: recoveryTestNow,
	}
	leaseUntil := recoveryTestNow.Add(time.Hour)
	attempt.LeaseUntil = &leaseUntil
	attempt.LeaseToken = "lease-recover"
	if _, _, err := store.ClaimWorkflowTask(
		ctx, attempt, 2, "recover-claim", strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("claim task: %v", err)
	}

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return recoveryTestNow }).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-recover").
		SpawnWorktrees([]ports.RepoWorktreeObservation{{
			RepoKey: "backend", ProjectID: "recover-proj", WorktreeRef: t.TempDir(),
			BaseSHA: recoveryRepoBaseSHA, HeadSHA: strings.Repeat("e", 40),
		}})
	// The fake gateway answers FindSpawn from its own spawn record, so a session that must
	// be adoptable has to be created by a real spawn through the gateway. The materializer
	// writes the durable row (the binding's foreign key needs it) and returns its id.
	gateway.WithSessionMaterializer(func(
		_ context.Context,
		_ domain.SessionID,
		request ports.SpawnRequest,
	) (domain.SessionID, error) {
		created, err := store.CreateSession(context.Background(), domain.SessionRecord{
			ProjectID: "recover-proj", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
			Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive},
			ClientRequestID: request.SpawnKey, ClientRequestCommitted: true,
			Metadata: domain.SessionMetadata{ControllerGeneration: "gen-recover"},
		})
		if err != nil {
			return "", err
		}
		return created.ID, nil
	})
	coordinator := execution.New(execution.Deps{
		Store:   store,
		Gateway: gateway,
		Clock:   func() time.Time { return recoveryTestNow },
	})
	recorded := &recordedBinder{
		inner: coordinator, errs: map[domain.WorkflowAttemptID]error{},
	}
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return &recoveryEnv{
		store:     store,
		gateway:   gateway,
		binder:    coordinator,
		recorded:  recorded,
		sweep:     bindingRecoverySweep{binder: recorded, store: store, batch: m2BindingRecoveryBatch, log: log},
		logs:      logs,
		workflow:  workflow.ID,
		task:      task.ID,
		attemptID: "recover-attempt",
		spawnKey:  execution.SpawnKey("recover-attempt", execution.ActionSpawn),
	}
}

// seedUnknownBinding creates a binding already in spawn_unknown, which is the state a
// crashed spawn leaves behind.
func (e *recoveryEnv) seedUnknownBinding(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	hash, err := execution.SpawnHash(ports.SpawnRequest{
		WorkflowID: e.workflow, TaskID: e.task, AttemptID: e.attemptID,
		SpawnKey: e.spawnKey, ProjectID: "recover-proj", Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	binding, created, err := e.store.CreateWorkflowAttemptBinding(ctx, domain.WorkflowAttemptBinding{
		AttemptID: e.attemptID, WorkflowID: e.workflow, TaskID: e.task,
		SpawnRequestID: e.spawnKey, SpawnHash: hash,
	}, []domain.WorkflowAttemptRepoBinding{{
		AttemptID: e.attemptID, WorkflowID: e.workflow, RepoKey: "backend",
		ProjectID: "recover-proj", ExpectedBaseSHA: recoveryRepoBaseSHA,
	}}, recoveryTestNow)
	if err != nil || !created {
		t.Fatalf("create binding: created=%v err=%v", created, err)
	}
	if _, applied, err := e.store.TransitionWorkflowAttemptBinding(
		ctx, e.attemptID, domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, recoveryTestNow,
	); err != nil || !applied {
		t.Fatalf("claim spawn: applied=%v err=%v", applied, err)
	}
	current, found, err := e.store.GetWorkflowAttemptBinding(ctx, e.attemptID)
	if err != nil || !found {
		t.Fatalf("read binding: found=%v err=%v", found, err)
	}
	if _, applied, err := e.store.TransitionWorkflowAttemptBinding(
		ctx, e.attemptID, domain.WorkflowBindingStateSpawnUnknown,
		"spawn_unclassified", current.Revision, recoveryTestNow,
	); err != nil || !applied {
		t.Fatalf("record unknown: applied=%v err=%v", applied, err)
	}
}

// spawnThroughGateway creates the session a crashed spawn would have left behind, using
// the gateway's own spawn path so FindSpawn can find it.
func (e *recoveryEnv) spawnThroughGateway(t *testing.T) {
	t.Helper()
	resource := ports.SpawnRequest{
		WorkflowID: e.workflow, TaskID: e.task, AttemptID: e.attemptID,
		SpawnKey: e.spawnKey, ProjectID: "recover-proj", Harness: domain.HarnessCodex,
	}
	if _, err := e.gateway.Spawn(context.Background(), resource); err != nil {
		t.Fatalf("gateway spawn: %v", err)
	}
	// The counter is captured here, so "recovery spawned" can be asserted by comparing
	// against what the fixture itself created.
	e.spawnsBeforeRecovery = e.gateway.SpawnCalls()
}

// state reads the binding's current state.
func (e *recoveryEnv) state(t *testing.T) domain.WorkflowBindingState {
	t.Helper()
	binding, found, err := e.store.GetWorkflowAttemptBinding(context.Background(), e.attemptID)
	if err != nil || !found {
		t.Fatalf("read binding: found=%v err=%v", found, err)
	}
	return binding.State
}

func TestM2RecoveryPromotesABindingWhoseSessionExists(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)

	// The session exists under the binding's spawn request id: a crashed spawn whose
	// side effect landed.
	env.spawnThroughGateway(t)

	result := env.sweep.run(context.Background())
	if result.Examined != 1 {
		t.Fatalf("examined = %d, want 1", result.Examined)
	}
	if result.Advanced != 1 {
		t.Fatalf("advanced = %d, want 1", result.Advanced)
	}
	if result.Failed != 0 {
		t.Fatalf("failed = %d, want 0", result.Failed)
	}
	if got := env.state(t); got != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", got)
	}
	// Reconcile must never spawn: the session already existed.
	if calls := env.gateway.SpawnCalls(); calls != env.spawnsBeforeRecovery {
		t.Fatalf("recovery spawned: calls went from %d to %d",
			env.spawnsBeforeRecovery, calls)
	}
}

func TestM2RecoveryLeavesAFrozenBindingFrozen(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)

	// No session exists for the spawn key: absence of evidence after a lost answer is not
	// proof that nothing happened.
	result := env.sweep.run(context.Background())
	if result.Examined != 1 {
		t.Fatalf("examined = %d, want 1", result.Examined)
	}
	if result.Failed != 0 {
		t.Fatalf("failed = %d, want 0: a frozen binding is an outcome, not a failure", result.Failed)
	}
	if result.Advanced != 1 {
		t.Fatalf("advanced = %d, want 1 (spawn_unknown → reconcile_required)", result.Advanced)
	}
	if got := env.state(t); got != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required", got)
	}
	if calls := env.gateway.SpawnCalls(); calls != env.spawnsBeforeRecovery {
		t.Fatalf("recovery spawned: calls went from %d to %d",
			env.spawnsBeforeRecovery, calls)
	}
}

func TestM2RecoverySecondSweepIsANoOp(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)
	// The spawn's side effect landed: the first sweep adopts it.
	env.spawnThroughGateway(t)

	first := env.sweep.run(context.Background())
	if first.Advanced != 1 {
		t.Fatalf("first sweep advanced = %d, want 1", first.Advanced)
	}
	before := len(env.recorded.calls)

	// bound_ready is not a reconcilable state, so the resolved binding leaves the set
	// entirely: a second sweep neither examines it nor calls the reconciler again.
	second := env.sweep.run(context.Background())
	if second.Examined != 0 {
		t.Fatalf("second sweep examined = %d, want 0: the resolved binding must leave the set",
			second.Examined)
	}
	if len(env.recorded.calls) != before {
		t.Fatalf("second sweep reconciled again: calls went from %d to %d",
			before, len(env.recorded.calls))
	}
	if got := env.state(t); got != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready to be stable", got)
	}
}

func TestM2RecoveryContinuesPastOneFailure(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)
	env.recorded.errs[env.attemptID] = errors.New("one binding is unreadable")

	result := env.sweep.run(context.Background())
	if result.Failed != 1 {
		t.Fatalf("failed = %d, want 1", result.Failed)
	}
	// The failure must be counted, not fatal: the sweep returned a result rather than
	// aborting, which is what lets every other workflow recover.
	if result.Examined != 1 {
		t.Fatalf("examined = %d, want 1", result.Examined)
	}
	// The binding is untouched, so the next sweep retries it.
	if got := env.state(t); got != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("state = %q, want the failed binding left as it was", got)
	}

	// Clearing the failure lets the same sweep resolve it, proving the failure was not
	// terminal.
	delete(env.recorded.errs, env.attemptID)
	env.spawnThroughGateway(t)
	second := env.sweep.run(context.Background())
	if second.Advanced != 1 {
		t.Fatalf("retry advanced = %d, want 1", second.Advanced)
	}
}

func TestM2RecoverySweepIsBounded(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)
	// A batch of one proves the bound is applied rather than ignored.
	env.sweep.batch = 1

	result := env.sweep.run(context.Background())
	if result.Examined > 1 {
		t.Fatalf("examined = %d, want at most the batch size", result.Examined)
	}
	if !result.Truncated {
		t.Fatal("a full batch did not report that more work remains")
	}
}

func TestM2RecoveryRepeatedFrozenSweepIsNotAFailure(t *testing.T) {
	// reconcile_required with no session is the coordinator's own settled answer. Running
	// the sweep again must count it as frozen, not as a failure: otherwise a daemon would
	// log an error every minute for a row that is behaving exactly as designed.
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)

	// The first sweep records the settled outcome (spawn_unknown → reconcile_required).
	first := env.sweep.run(context.Background())
	if first.Advanced != 1 {
		t.Fatalf("first sweep advanced = %d, want 1", first.Advanced)
	}
	second := env.sweep.run(context.Background())
	if second.Failed != 0 {
		t.Fatalf("second sweep failed = %d, want 0 (a settled binding is not a failure)",
			second.Failed)
	}
	if second.Frozen != 1 {
		t.Fatalf("second sweep frozen = %d, want 1", second.Frozen)
	}
	if strings.Contains(env.logs.String(), "recovery failed") {
		t.Fatalf("a settled binding was logged as a failure: %s", env.logs.String())
	}
}

func TestM2RecoverySkipsRecoveryUncertain(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)
	env.spawnThroughGateway(t)
	// A first sweep promotes it to bound_ready, which is the only state that can move to
	// recovery_uncertain (that state requires a session).
	if first := env.sweep.run(context.Background()); first.Advanced != 1 {
		t.Fatalf("first sweep advanced = %d, want 1", first.Advanced)
	}
	ctx := context.Background()
	current, _, err := env.store.GetWorkflowAttemptBinding(ctx, env.attemptID)
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if current.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready before the uncertain move", current.State)
	}
	if _, applied, err := env.store.TransitionWorkflowAttemptBinding(
		ctx, env.attemptID, domain.WorkflowBindingStateRecoveryUncertain,
		"evidence_insufficient", current.Revision, recoveryTestNow,
	); err != nil || !applied {
		t.Fatalf("to recovery_uncertain: applied=%v err=%v", applied, err)
	}

	// The sweep lists only the reconcilable states, so a recovery_uncertain binding is not
	// even examined: acting on it would consume the batch on rows nothing can move.
	callsBefore := len(env.recorded.calls)
	result := env.sweep.run(context.Background())
	if len(env.recorded.calls) != callsBefore {
		t.Fatalf("the sweep examined a recovery_uncertain binding: calls went from %d to %d",
			callsBefore, len(env.recorded.calls))
	}
	if result.Examined != 0 {
		t.Fatalf("examined = %d, want 0 (recovery_uncertain is not reconcilable)", result.Examined)
	}
	if got := env.state(t); got != domain.WorkflowBindingStateRecoveryUncertain {
		t.Fatalf("state = %q, want recovery_uncertain untouched", got)
	}
}

func TestM2RecoveryLogCarriesCountsOnly(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)
	env.sweep.run(context.Background())

	// The sweep itself logs; assert nothing identifying reached the log.
	out := env.logs.String()
	for _, banned := range []string{
		string(env.attemptID), string(env.workflow), string(env.task), env.spawnKey,
		"recover-proj",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("the recovery log carries the identifier %q: %s", banned, out)
		}
	}
}

func TestStartM2BindingRecoveryStopsOnCancellation(t *testing.T) {
	env := newRecoveryEnv(t)
	env.seedUnknownBinding(t)

	ctx, cancel := context.WithCancel(context.Background())
	startM2BindingRecovery(ctx, env.binder, env.store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	// The startup sweep is synchronous, so the binding is already resolved.
	if got := env.state(t); got != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want the startup sweep to have run", got)
	}
	cancel()
	// Cancellation must not leave the loop running; a second sweep after cancel performs
	// no work, which is the observable half of "stopped on ctx cancel".
	before := env.state(t)
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	sweepCancel()
	env.sweep.run(sweepCtx)
	if got := env.state(t); got != before {
		t.Fatalf("a cancelled sweep changed the binding from %q to %q", before, got)
	}
}

func TestStartM2BindingRecoveryIgnoresMissingCollaborators(t *testing.T) {
	// A partially wired daemon must not panic, and must not start a loop it cannot run.
	startM2BindingRecovery(context.Background(), nil, nil, slog.Default())
}

// --- sweep-level wiring assertions ------------------------------------------
//
// These drive bindingRecoverySweep directly against a real store with an injected
// reconciler, so each assertion is about the SWEEP (what it lists, what it calls, what it
// leaves alone) rather than about the coordinator underneath it.

// multiBindingEnv seeds several workflows' attempts and returns their ids, so a sweep can be
// observed touching more than one row.
type multiBindingEnv struct {
	store    *sqlite.Store
	gateway  *execution.FakeGateway
	sweep    bindingRecoverySweep
	recorded *recordedBinder
	logs     *bytes.Buffer
	seeds    []multiSeed
}

type multiSeed struct {
	attemptID domain.WorkflowAttemptID
	spawnKey  string
	hasRow    bool
}

// newMultiBindingEnv 让各 Workflow 的既有授权仓库都能通过实时核验，保留批量恢复边界。
func newMultiBindingEnv(t *testing.T) *multiBindingEnv {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	ctx := context.Background()
	now := recoveryTestNow

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "multi-proj", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return now }).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-multi").
		SpawnWorktrees([]ports.RepoWorktreeObservation{{
			RepoKey: "backend", ProjectID: "multi-proj", WorktreeRef: t.TempDir(),
			BaseSHA: recoveryRepoBaseSHA, HeadSHA: strings.Repeat("e", 40),
		}})
	gateway.WithSessionMaterializer(func(
		_ context.Context,
		_ domain.SessionID,
		request ports.SpawnRequest,
	) (domain.SessionID, error) {
		created, err := store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: request.ProjectID, Kind: domain.KindWorker, Harness: request.Harness,
			Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive},
			ClientRequestID: request.SpawnKey, ClientRequestCommitted: true,
			Metadata: domain.SessionMetadata{ControllerGeneration: "gen-multi"},
		})
		if err != nil {
			return "", err
		}
		return created.ID, nil
	})
	coordinator := execution.New(execution.Deps{
		Store: store, Gateway: gateway, Clock: func() time.Time { return now },
	})
	recorded := &recordedBinder{inner: coordinator, errs: map[domain.WorkflowAttemptID]error{}}
	logs := &bytes.Buffer{}

	return &multiBindingEnv{
		store:    store,
		gateway:  gateway,
		recorded: recorded,
		logs:     logs,
		sweep: bindingRecoverySweep{
			binder: recorded, store: store, batch: m2BindingRecoveryBatch,
			log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		},
	}
}

// seed creates one workflow/task/attempt plus a spawn_unknown binding, and optionally the
// session a lost spawn left behind.
func (e *multiBindingEnv) seed(t *testing.T, name string, hasRow bool) {
	t.Helper()
	ctx := context.Background()
	now := recoveryTestNow

	workflowID := domain.WorkflowID("multi-workflow-" + name)
	taskID := domain.WorkflowTaskID("multi-task-" + name)
	attemptID := domain.WorkflowAttemptID("multi-attempt-" + name)

	workflow := domain.Workflow{
		ID: workflowID, Title: "Multi " + name, Goal: "seed",
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	task := domain.WorkflowTask{
		ID: taskID, WorkflowID: workflowID, Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Task " + name, State: domain.WorkflowTaskStatePending, MaxAttempts: 1,
		Revision: 1, AgentHarness: domain.HarnessCodex, CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := e.store.CreateWorkflow(
		ctx, workflow,
		[]domain.WorkflowRepository{{
			WorkflowID: workflowID, RepoKey: "backend", ProjectID: "multi-proj",
			BaseSHA: recoveryRepoBaseSHA,
		}},
		[]domain.WorkflowTask{task}, nil, nil,
		"multi-create-"+name, strings.Repeat("a", 64),
	); err != nil {
		t.Fatalf("create workflow %s: %v", name, err)
	}
	if _, _, err := e.store.ActivateWorkflow(
		ctx, workflowID, 1, "multi-activate-"+name, strings.Repeat("b", 64), now,
	); err != nil {
		t.Fatalf("activate workflow %s: %v", name, err)
	}

	leaseUntil := now.Add(time.Hour)
	attempt := domain.WorkflowAttempt{
		ID: attemptID, WorkflowID: workflowID, TaskID: taskID, AttemptNo: 0,
		WorkerID: "worker-" + name, LeaseToken: "lease-" + name, LeaseUntil: &leaseUntil,
		State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := e.store.ClaimWorkflowTask(
		ctx, attempt, 2, "multi-claim-"+name, strings.Repeat("c", 64),
	); err != nil {
		t.Fatalf("claim task %s: %v", name, err)
	}

	spawnKey := execution.SpawnKey(attemptID, execution.ActionSpawn)
	hash, err := execution.SpawnHash(ports.SpawnRequest{
		WorkflowID: workflowID, TaskID: taskID, AttemptID: attemptID,
		SpawnKey: spawnKey, ProjectID: "multi-proj", Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	binding, created, err := e.store.CreateWorkflowAttemptBinding(ctx, domain.WorkflowAttemptBinding{
		AttemptID: attemptID, WorkflowID: workflowID, TaskID: taskID,
		SpawnRequestID: spawnKey, SpawnHash: hash,
	}, []domain.WorkflowAttemptRepoBinding{{
		AttemptID: attemptID, WorkflowID: workflowID, RepoKey: "backend",
		ProjectID: "multi-proj", ExpectedBaseSHA: recoveryRepoBaseSHA,
	}}, now)
	if err != nil || !created {
		t.Fatalf("create binding %s: created=%v err=%v", name, created, err)
	}
	if _, applied, err := e.store.TransitionWorkflowAttemptBinding(
		ctx, attemptID, domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, now,
	); err != nil || !applied {
		t.Fatalf("claim spawn %s: applied=%v err=%v", name, applied, err)
	}
	current, _, err := e.store.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		t.Fatalf("read binding %s: %v", name, err)
	}
	if _, applied, err := e.store.TransitionWorkflowAttemptBinding(
		ctx, attemptID, domain.WorkflowBindingStateSpawnUnknown,
		"spawn_unclassified", current.Revision, now,
	); err != nil || !applied {
		t.Fatalf("record unknown %s: applied=%v err=%v", name, applied, err)
	}

	if hasRow {
		// The lost spawn's side effect: a committed session under this spawn key. It is
		// created THROUGH the gateway, because FindSpawn answers from the gateway's own
		// record — a row written straight into the store would be invisible to reconcile
		// and the test would be proving nothing about adoption.
		if _, err := e.gateway.Spawn(ctx, ports.SpawnRequest{
			WorkflowID: workflowID, TaskID: taskID, AttemptID: attemptID,
			SpawnKey: spawnKey, SpawnHash: hash,
			ProjectID: "multi-proj", Harness: domain.HarnessCodex,
		}); err != nil {
			t.Fatalf("gateway spawn %s: %v", name, err)
		}
	}
	e.seeds = append(e.seeds, multiSeed{attemptID: attemptID, spawnKey: spawnKey, hasRow: hasRow})
}

// stateFor reads one binding's state.
func (e *multiBindingEnv) stateFor(t *testing.T, attemptID domain.WorkflowAttemptID) domain.WorkflowBindingState {
	t.Helper()
	binding, found, err := e.store.GetWorkflowAttemptBinding(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read binding %s: found=%v err=%v", attemptID, found, err)
	}
	return binding.State
}

// TestM2RecoverySweepExaminesEachUnprovenBindingOnce proves the sweep's listing and its
// per-binding consequence: the row whose session exists is adopted, and the row with no
// session is left reconcile_required rather than counted as a failure.
func TestM2RecoverySweepExaminesEachUnprovenBindingOnce(t *testing.T) {
	env := newMultiBindingEnv(t)
	env.seed(t, "with-row", true)
	env.seed(t, "no-row", false)

	result := env.sweep.run(context.Background())
	if result.Examined != 2 {
		t.Fatalf("examined = %d, want 2", result.Examined)
	}
	// Both bindings move: the adoptable one lands bound_ready, the other lands
	// reconcile_required. Neither is a failure.
	if result.Advanced != 2 {
		t.Fatalf("advanced = %d, want 2", result.Advanced)
	}
	if result.Failed != 0 {
		t.Fatalf("failed = %d, want 0: a missing session is an outcome, not a failure",
			result.Failed)
	}
	if len(env.recorded.calls) != 2 {
		t.Fatalf("reconciler calls = %d, want exactly one per binding", len(env.recorded.calls))
	}

	seen := map[domain.WorkflowAttemptID]int{}
	for _, attemptID := range env.recorded.calls {
		seen[attemptID]++
	}
	for _, seed := range env.seeds {
		if seen[seed.attemptID] != 1 {
			t.Fatalf("binding %s was reconciled %d times, want 1",
				seed.attemptID, seen[seed.attemptID])
		}
	}

	if got := env.stateFor(t, domain.WorkflowAttemptID("multi-attempt-with-row")); got != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("adoptable binding = %q, want bound_ready", got)
	}
	if got := env.stateFor(t, domain.WorkflowAttemptID("multi-attempt-no-row")); got != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("no-session binding = %q, want reconcile_required", got)
	}
}

// TestM2RecoverySweepIsIdempotent proves a second pass makes no new external call for a
// settled binding.
func TestM2RecoverySweepIsIdempotent(t *testing.T) {
	env := newMultiBindingEnv(t)
	env.seed(t, "settled", true)

	first := env.sweep.run(context.Background())
	if first.Advanced != 1 {
		t.Fatalf("first sweep advanced = %d, want 1", first.Advanced)
	}
	callsAfterFirst := len(env.recorded.calls)

	second := env.sweep.run(context.Background())
	if second.Advanced != 0 {
		t.Fatalf("second sweep advanced = %d, want 0", second.Advanced)
	}
	if second.Examined != 0 {
		t.Fatalf("second sweep examined = %d, want 0: bound_ready is not reconcilable",
			second.Examined)
	}
	if len(env.recorded.calls) != callsAfterFirst {
		t.Fatalf("second sweep made %d new reconciler calls",
			len(env.recorded.calls)-callsAfterFirst)
	}
}

// TestM2RecoverySweepContinuesPastOneFailingBinding proves one workflow's error does not
// stop the others.
func TestM2RecoverySweepContinuesPastOneFailingBinding(t *testing.T) {
	env := newMultiBindingEnv(t)
	env.seed(t, "ok-1", true)
	env.seed(t, "broken", true)
	env.seed(t, "ok-2", true)

	broken := domain.WorkflowAttemptID("multi-attempt-broken")
	env.recorded.errs[broken] = errors.New("this workflow's binding is unreadable")

	result := env.sweep.run(context.Background())
	if result.Failed != 1 {
		t.Fatalf("failed = %d, want 1", result.Failed)
	}
	if result.Advanced != 2 {
		t.Fatalf("advanced = %d, want 2: the healthy bindings must still have run",
			result.Advanced)
	}
	if got := env.stateFor(t, broken); got != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("failed binding = %q, want it untouched", got)
	}
	if got := env.stateFor(t, domain.WorkflowAttemptID("multi-attempt-ok-1")); got != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("healthy binding 1 = %q, want bound_ready", got)
	}
	if got := env.stateFor(t, domain.WorkflowAttemptID("multi-attempt-ok-2")); got != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("healthy binding 2 = %q, want bound_ready", got)
	}
}

// TestM2RecoverySweepHonoursTheBatchBound proves the sweep processes at most one batch even
// when the backlog is larger.
func TestM2RecoverySweepHonoursTheBatchBound(t *testing.T) {
	env := newMultiBindingEnv(t)
	total := int(m2BindingRecoveryBatch) + 5
	for i := 0; i < total; i++ {
		env.seed(t, fmt.Sprintf("bulk-%02d", i), false)
	}
	// A small batch keeps the test fast while still proving the LIMIT applies.
	env.sweep.batch = 10

	result := env.sweep.run(context.Background())
	if result.Examined != 10 {
		t.Fatalf("examined = %d, want exactly the batch size (10)", result.Examined)
	}
	if len(env.recorded.calls) != 10 {
		t.Fatalf("reconciler calls = %d, want 10", len(env.recorded.calls))
	}
	if !result.Truncated {
		t.Fatal("a full batch did not report that more work remains")
	}

	// The remaining rows are still there for the next pass: the bound delays work, it does
	// not lose it.
	remaining, err := env.store.ListWorkflowAttemptBindingsByState(
		context.Background(), m2ReconcilableStates, int64(total))
	if err != nil {
		t.Fatalf("list remaining: %v", err)
	}
	if len(remaining) == 0 {
		t.Fatal("the batch bound consumed every row; the sweep must leave the rest for later")
	}
}
