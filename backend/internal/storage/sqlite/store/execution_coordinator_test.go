package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Integration coverage for the M2-04 coordinator against the real M2-03 store.
//
// The coordinator's unit tests use an in-memory BindingStore so a failure points at
// coordinator logic. These tests exist for the other half: the coordinator's real method
// set must satisfy the real store's CAS and transaction semantics. In particular the
// cross-table write (binding.session_id and workflow_attempts.session_id in one
// transaction) is only exercised here.

func executionTestNow() time.Time {
	return time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
}

// permitAllCodex builds a capability resolver that allows the codex harness at
// handshake grade, so these tests are about execution wiring rather than capability.
func permitAllCodex(now time.Time) *capability.Resolver {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		domain.HarnessCodex, domain.CapabilitySourceProviderHandshake, now, capability.DefaultTTL,
	)
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Streaming = domain.CapabilitySupported
	snapshot.Tools = domain.CapabilitySupported
	snapshot.Interrupt = domain.CapabilitySupported
	snapshot.Steer = domain.CapabilitySupported
	snapshot.Queue = domain.CapabilitySupported
	snapshot.Resume = domain.CapabilityResumeWithHistory
	snapshot.Approval = domain.CapabilityApprovalEnforced
	snapshot.ApplyAt = domain.CapabilityApplyAtTurn
	snapshot.Usage = domain.CapabilityUsageExact

	probe := capability.NewFakeProbe().Script(snapshot)
	resolver := capability.NewResolver(capability.WithClock(func() time.Time { return now }))
	resolver.RegisterProbe(domain.HarnessCodex, probe)
	return resolver
}

func executionSpawnRequest(attempt domain.WorkflowAttempt, projectID string) ports.SpawnRequest {
	return ports.SpawnRequest{
		WorkflowID: attempt.WorkflowID,
		TaskID:     attempt.TaskID,
		AttemptID:  attempt.ID,
		ProjectID:  domain.ProjectID(projectID),
		Harness:    domain.HarnessCodex,
		Mode:       domain.SessionModeChat,
		Prompt:     "implement the task",
		Repos: []ports.WorkflowRepoKeyRef{{
			RepoKey: "backend", ProjectID: domain.ProjectID(projectID), BaseSHA: "base-sha-1",
		}},
	}
}

// TestExecutionCoordinatorBindsAgainstRealStore proves the full guarded sequence works
// against real SQL: gate, intent, spawn, cross-table bind.
func TestExecutionCoordinatorBindsAgainstRealStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := executionTestNow()
	projectID := "exec-project"
	workflowID := domain.WorkflowID("workflow-exec-bind")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-exec-a", projectID, now)

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return now }).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID)).
		SpawnReadiness(ports.SessionReadinessProvisioning).
		SpawnGeneration("gen-exec-1")
	coordinator := execution.New(execution.Deps{
		Store:    s,
		Gateway:  gateway,
		Resolver: permitAllCodex(now),
		Clock:    func() time.Time { return now },
	})

	repos := []domain.WorkflowAttemptRepoBinding{{
		RepoKey: "backend", ProjectID: domain.ProjectID(projectID),
		ExpectedBaseSHA: "base-sha-1", State: domain.WorkflowRepoBindingStatePending,
	}}
	outcome, err := coordinator.BindAttempt(ctx, executionSpawnRequest(attempt, projectID), repos)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if !outcome.Spawned {
		t.Fatalf("outcome = %+v, want a spawned binding", outcome)
	}
	// The corrected semantics: a provisioning spawn records session_provisioning, which
	// names the session honestly instead of claiming the session was ready.
	if outcome.Binding.State != domain.WorkflowBindingStateSessionProvisioning {
		t.Fatalf("state = %q, want session_provisioning", outcome.Binding.State)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1", gateway.SpawnCalls())
	}

	// The binding is durable and carries a session.
	stored, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	if stored.SessionID == nil {
		t.Fatal("the durable binding has no session")
	}
	if stored.ControllerGeneration != "gen-exec-1" {
		t.Fatalf("generation = %q, want gen-exec-1", stored.ControllerGeneration)
	}
	// The M1 attempt row agrees, in the same transaction.
	storedAttempt, _, err := s.GetWorkflowAttempt(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if storedAttempt.SessionID == nil || *storedAttempt.SessionID != *stored.SessionID {
		t.Fatalf("attempt session = %v, binding session = %v; the two truths diverged",
			storedAttempt.SessionID, stored.SessionID)
	}
	// The repo expectation was written with the intent.
	repoRows, err := s.ListWorkflowAttemptRepoBindings(ctx, attempt.ID)
	if err != nil || len(repoRows) != 1 {
		t.Fatalf("repo bindings = %d err=%v, want 1", len(repoRows), err)
	}
	if repoRows[0].ExpectedBaseSHA != "base-sha-1" {
		t.Fatalf("expected base = %q, want base-sha-1", repoRows[0].ExpectedBaseSHA)
	}
	// Nothing reached running.
	if stored.State == domain.WorkflowBindingStateRunning {
		t.Fatal("the offline path promoted the binding to running")
	}
}

// TestExecutionCoordinatorLostSpawnIsDurableAndNotRetried proves the single-spawn
// guarantee survives the real store, including across a fresh coordinator instance
// (which is how a restart looks).
func TestExecutionCoordinatorLostSpawnIsDurableAndNotRetried(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := executionTestNow()
	projectID := "exec-lost-project"
	workflowID := domain.WorkflowID("workflow-exec-lost")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-exec-lost", projectID, now)

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return now }).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID)).
		LoseSpawnResponse()
	coordinator := execution.New(execution.Deps{
		Store:    s,
		Gateway:  gateway,
		Resolver: permitAllCodex(now),
		Clock:    func() time.Time { return now },
	})

	request := executionSpawnRequest(attempt, projectID)
	outcome, err := coordinator.BindAttempt(ctx, request, nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if !outcome.Frozen || outcome.Binding.State != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("outcome = %+v, want a frozen spawn_unknown", outcome)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1", gateway.SpawnCalls())
	}

	// A fresh coordinator over the same store is the restart case. It must read the
	// durable unknown and refuse, without the gateway being asked again.
	restarted := execution.New(execution.Deps{
		Store:    s,
		Gateway:  gateway,
		Resolver: permitAllCodex(now),
		Clock:    func() time.Time { return now },
	})
	retry, err := restarted.BindAttempt(ctx, request, nil)
	if err != nil {
		t.Fatalf("retry BindAttempt: %v", err)
	}
	if retry.Spawned {
		t.Fatal("the restarted coordinator spawned again")
	}
	if !retry.Frozen {
		t.Fatal("the restarted coordinator did not see the frozen state")
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls after restart = %d, want 1", gateway.SpawnCalls())
	}

	// Durable state confirms the freeze, so any later reader sees the same fact.
	stored, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	if stored.State != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("durable state = %q, want spawn_unknown", stored.State)
	}
	if stored.SessionID != nil {
		t.Fatal("an unknown binding recorded a session it never observed")
	}
	storedAttempt, _, err := s.GetWorkflowAttempt(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if storedAttempt.SessionID != nil {
		t.Fatal("an unknown binding wrote a session onto the attempt")
	}
}

// TestExecutionCoordinatorStorageRejectionRollsBack proves a rejected repo key aborts the
// whole intent against the real store, leaving no binding behind.
func TestExecutionCoordinatorStorageRejectionRollsBack(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := executionTestNow()
	projectID := "exec-reject-project"
	workflowID := domain.WorkflowID("workflow-exec-reject")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-exec-reject", projectID, now)

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return now }).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID))
	coordinator := execution.New(execution.Deps{
		Store:    s,
		Gateway:  gateway,
		Resolver: permitAllCodex(now),
		Clock:    func() time.Time { return now },
	})

	// "frontend" was never registered on this workflow, so the composite foreign key
	// rejects the repo binding and the store rolls the intent back.
	repos := []domain.WorkflowAttemptRepoBinding{{
		RepoKey: "frontend", ProjectID: domain.ProjectID(projectID),
		ExpectedBaseSHA: "base-sha-1", State: domain.WorkflowRepoBindingStatePending,
	}}
	_, err := coordinator.BindAttempt(ctx, executionSpawnRequest(attempt, projectID), repos)
	if !errors.Is(err, domain.ErrWorkflowBindingRepoNotInWorkflow) {
		t.Fatalf("err = %v, want ErrWorkflowBindingRepoNotInWorkflow", err)
	}
	if gateway.SpawnCalls() != 0 {
		t.Fatalf("spawn calls = %d, want 0: a rejected intent must not reach the gateway", gateway.SpawnCalls())
	}
	if _, found, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID); err != nil || found {
		t.Fatalf("binding survived a rejected intent: found=%v err=%v", found, err)
	}
}

// TestExecutionCoordinatorStableSpawnKeyAcrossRestart proves the derivation is a pure
// function, so a restarted coordinator computes the same key and the store replays.
func TestExecutionCoordinatorStableSpawnKeyAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := executionTestNow()
	projectID := "exec-key-project"
	workflowID := domain.WorkflowID("workflow-exec-key")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-exec-key", projectID, now)

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return now }).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID))
	newCoordinator := func() *execution.Coordinator {
		return execution.New(execution.Deps{
			Store:    s,
			Gateway:  gateway,
			Resolver: permitAllCodex(now),
			Clock:    func() time.Time { return now },
		})
	}

	request := executionSpawnRequest(attempt, projectID)
	if _, err := newCoordinator().BindAttempt(ctx, request, nil); err != nil {
		t.Fatalf("first BindAttempt: %v", err)
	}
	firstKey := gateway.LastSpawn().SpawnKey
	if firstKey == "" {
		t.Fatal("the gateway received an empty spawn key")
	}

	if _, err := newCoordinator().BindAttempt(ctx, request, nil); err != nil {
		t.Fatalf("second BindAttempt: %v", err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1: the replay must not reach the gateway", gateway.SpawnCalls())
	}
	// The key is reproducible from the attempt alone, which is what makes a restart safe.
	if got := execution.SpawnKey(attempt.ID, execution.ActionSpawn); got != firstKey {
		t.Fatalf("SpawnKey = %q, want the key the first call used %q", got, firstKey)
	}
	if !strings.HasPrefix(firstKey, "spawn-") {
		t.Fatalf("spawn key %q does not name its action", firstKey)
	}
}

// TestExecutionCoordinatorLeaseStaysOnAttempt proves the coordinator's flow never moves
// the workflow lease into the binding or the observation.
func TestExecutionCoordinatorLeaseStaysOnAttempt(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := executionTestNow()
	projectID := "exec-lease-project"
	workflowID := domain.WorkflowID("workflow-exec-lease")
	attempt := seedClaimedAttempt(t, s, workflowID, "task-exec-lease", projectID, now)
	if attempt.LeaseToken == "" {
		t.Fatal("fixture problem: the attempt has no lease token")
	}

	gateway := execution.NewFakeGateway().
		WithClock(func() time.Time { return now }).
		WithSessionMaterializer(sessionMaterializer(t, s, projectID))
	coordinator := execution.New(execution.Deps{
		Store:    s,
		Gateway:  gateway,
		Resolver: permitAllCodex(now),
		Clock:    func() time.Time { return now },
	})
	if _, err := coordinator.BindAttempt(ctx, executionSpawnRequest(attempt, projectID), nil); err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}

	// The observation the gateway produced is what the coordinator saw; it must not
	// contain the lease capability.
	observation, _, err := coordinator.ObserveSession(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("ObserveSession: %v", err)
	}
	rendered := strings.Join([]string{
		string(observation.SessionID), observation.ControllerGeneration,
		observation.ClientRequestID, observation.ProviderConversationID,
		string(observation.Readiness),
	}, "|")
	if strings.Contains(rendered, attempt.LeaseToken) {
		t.Fatal("the session observation leaked the workflow lease token")
	}
	// And the attempt still holds its lease: binding does not consume it.
	storedAttempt, _, err := s.GetWorkflowAttempt(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if storedAttempt.LeaseToken != attempt.LeaseToken {
		t.Fatal("the coordinator changed the attempt's lease")
	}
}

// sessionMaterializer creates a real session row for the id the fake gateway is about to
// report, so the binding's foreign key into sessions is satisfied by the database rather
// than bypassed.
func sessionMaterializer(
	t *testing.T,
	s *sqlite.Store,
	projectID string,
) func(ctx context.Context, sessionID domain.SessionID, request ports.SpawnRequest) (domain.SessionID, error) {
	t.Helper()
	return func(_ context.Context, _ domain.SessionID, _ ports.SpawnRequest) (domain.SessionID, error) {
		rec := sampleRecord(projectID)
		created, err := s.CreateSession(context.Background(), rec)
		if err != nil {
			return "", err
		}
		return created.ID, nil
	}
}
