package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
)

// 删除恢复前占位接管或改成再次 Spawn，会使丢回执测试重复创建；旧任期回执必须被 Store 拒绝。
func TestM3CoordinatorRecoversAcceptedSpawnWithoutReexecution(t *testing.T) {
	ctx := context.Background()
	s, attempt := newReanchorStore(t)
	now := reanchorNow
	leader, err := s.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldFence := leader.Fence
	gateway := execution.NewFakeGateway().WithClock(func() time.Time { return now }).SpawnReadiness(ports.SessionReadinessReady).SpawnGeneration("gen-1").LoseSpawnResponse()
	gateway.WithSessionMaterializer(func(ctx context.Context, _ domain.SessionID, request ports.SpawnRequest) (domain.SessionID, error) {
		p, ok := ports.ExecutionPermitFromContext(ctx)
		if !ok {
			return "", domain.ErrAdmissionRequired
		}
		ad, found, err := s.GetSchedulerAdmission(ctx, p.AdmissionID)
		if err != nil {
			return "", err
		}
		if !found {
			return "", domain.ErrAdmissionRequired
		}
		rec, _, err := s.CreateAdmittedSession(ctx, domain.SessionRecord{ProjectID: reanchorProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: request.SpawnKey, ClientRequestHash: request.SpawnHash, CreatedAt: now, UpdatedAt: now, Metadata: domain.SessionMetadata{ControllerGeneration: "gen-1"}, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}}, ad, "", now)
		return rec.ID, err
	})
	coordinator := execution.New(execution.Deps{Store: s, Gateway: gateway, Clock: func() time.Time { return now }, Resolver: reanchorResolver(), ExecutionFence: func(context.Context) (domain.SchedulerFence, error) { return oldFence, nil }})
	request := ports.SpawnRequest{WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID, AttemptID: attempt.ID, ProjectID: reanchorProject, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Prompt: "fake local work"}
	out, err := coordinator.BindAttempt(ctx, request, nil)
	if err != nil || !out.Frozen || gateway.SpawnCalls() != 1 {
		t.Fatalf("lost receipt: %+v calls=%d err=%v", out, gateway.SpawnCalls(), err)
	}
	if _, err := coordinator.BindAttempt(ctx, request, nil); err != nil || gateway.SpawnCalls() != 1 {
		t.Fatalf("replayed unknown spawned: %d %v", gateway.SpawnCalls(), err)
	}
	ads, _ := s.ListSchedulerAdmissions(ctx, true)
	if len(ads) != 1 || ads[0].SessionID == "" {
		t.Fatalf("accepted session lost its slot: %+v", ads)
	}
	oldPermit, err := s.LoadExecutionPermit(ctx, attempt.ID, oldFence, now)
	if err != nil {
		t.Fatal(err)
	}
	now = leader.LeaseUntil.Add(time.Second)
	newLeader, changed, err := s.AcquireSchedulerLeader(ctx, "replacement", now, now.Add(time.Minute))
	if err != nil || !changed || newLeader.Recovered {
		t.Fatalf("new leader incorrectly recovered: %+v %v", newLeader, err)
	}
	observation := gateway.SpawnSessions()[0]
	observation.ObservedAt = now
	gateway.SetSessionObservation(observation)
	if err := coordinator.RecoverAdmissions(ctx, newLeader.Fence); err != nil {
		t.Fatal(err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatal("recovery repeated spawn")
	}
	bound, _, err := s.GetWorkflowAttemptBinding(ctx, attempt.ID)
	if err != nil || bound.SessionID == nil || *bound.SessionID != ads[0].SessionID {
		t.Fatalf("lost receipt not rebound: %+v %v", bound, err)
	}
	ad, _, _ := s.GetSchedulerAdmission(ctx, ads[0].ID)
	if ad.Fence != newLeader.Fence || ad.State == domain.AdmissionReleased {
		t.Fatalf("old occupied slot lost: %+v", ad)
	}
	if _, _, err := s.ReanchorWorkflowAttemptGeneration(ports.WithExecutionPermit(ctx, oldPermit), attempt.ID, *bound.SessionID, "late", bound.Revision, bound.State, now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch receipt accepted: %v", err)
	}
	if _, _, err := s.CompleteSchedulerRecovery(ctx, newLeader.Fence, newLeader.Revision, now); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.GetWorkflowAttempt(ctx, attempt.ID)
	if err := coordinator.RefreshLeases(ctx, newLeader.Fence); err != nil {
		t.Fatal(err)
	}
	after, _, _ := s.GetWorkflowAttempt(ctx, attempt.ID)
	if !before.LeaseUntil.Equal(*after.LeaseUntil) {
		t.Fatal("expired lease was renewed by recovery")
	}
	if _, err := coordinator.BindAttempt(ctx, request, nil); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old coordinator borrowed new owner: %v", err)
	}
}
