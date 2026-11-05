package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// 删除 Harness 持久冷却、最新 Attempt 绑定或 safe-to-retry 分类检查，会放行这里的拒绝路径。
func TestVerifiedRetryPersistsCooldownAndCannotAuthorizeLaterFailure(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	f.policy(domain.HarnessCodex, 10)
	w, _, err := f.workflow.Create(context.Background(), "retry-create", workflow.CreateInput{Title: "Retry", Goal: "Verified failure only",
		Repositories: []workflow.RepositoryInput{{RepoKey: "repo", ProjectID: "project"}}, Tasks: []workflow.TaskInput{
			{ID: "retry-target", Title: "Target", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, MaxAttempts: 5},
			{ID: "sibling", Title: "Sibling", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex},
		}})
	if err != nil {
		t.Fatal(err)
	}
	w, _, err = f.workflow.Activate(context.Background(), w.ID, w.Revision, "retry-activate")
	if err != nil {
		t.Fatal(err)
	}
	claim := func(key string) domain.WorkflowAttempt {
		t.Helper()
		task, _, readErr := f.store.GetWorkflowTask(context.Background(), w.ID, "retry-target")
		if readErr != nil {
			t.Fatal(readErr)
		}
		result, created, claimErr := f.workflow.Claim(context.Background(), workflow.ClaimInput{WorkflowID: w.ID, TaskID: task.ID, ExpectedRevision: task.Revision, WorkerID: "worker", IdempotencyKey: key})
		if claimErr != nil || !created {
			t.Fatalf("claim: %v %v", created, claimErr)
		}
		return result.Attempt
	}
	fail := func(a domain.WorkflowAttempt, key string) {
		t.Helper()
		if _, _, settleErr := f.workflow.Settle(f.permit(a.ID), workflow.SettleInput{WorkflowID: w.ID, AttemptID: a.ID, LeaseToken: a.LeaseToken, Result: domain.WorkflowAttemptStateFailed, IdempotencyKey: key}); settleErr != nil {
			t.Fatal(settleErr)
		}
	}
	retry := func(key string) domain.WorkflowTask {
		t.Helper()
		task, _, readErr := f.store.GetWorkflowTask(context.Background(), w.ID, "retry-target")
		if readErr != nil {
			t.Fatal(readErr)
		}
		task, _, retryErr := f.workflow.Retry(context.Background(), workflow.RetryInput{WorkflowID: w.ID, TaskID: task.ID, ExpectedRevision: task.Revision, IdempotencyKey: key})
		if retryErr != nil {
			t.Fatal(retryErr)
		}
		return task
	}
	a := claim("first")
	fail(a, "failed-first")
	request := VerifiedRetry{WorkflowID: w.ID, TaskID: a.TaskID, AttemptID: a.ID, ReasonCode: "UNKNOWN", RetryAfter: f.clock.Now().Add(45 * time.Second)}
	if _, err := f.scheduler.ScheduleVerifiedRetry(context.Background(), request); err == nil {
		t.Fatal("unknown classification accepted")
	}
	request.ReasonCode = "RATE_LIMITED"
	due, err := f.scheduler.ScheduleVerifiedRetry(context.Background(), request)
	if err != nil || due.NextEligibleAt.Before(request.RetryAfter) || due.AttemptID != a.ID {
		t.Fatalf("retry due=%+v %v", due, err)
	}
	request.RetryAfter = time.Time{}
	repeated, err := f.scheduler.ScheduleVerifiedRetry(context.Background(), request)
	if err != nil || !repeated.NextEligibleAt.Equal(due.NextEligibleAt) {
		t.Fatalf("repeated retry shortened durable Retry-After: %+v %v", repeated, err)
	}
	f.clock.Advance(31 * time.Second)
	f.connect("scheduler-after-restart")
	f.recover()
	sibling, _, err := f.store.GetWorkflowTask(context.Background(), w.ID, "sibling")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.scheduler.PrepareWorkflowAdmission(context.Background(), sibling); !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("cooldown lost at restart: %v", err)
	}
	f.clock.Advance(15 * time.Second)
	if _, err := f.scheduler.MaintainLeadership(context.Background()); err != nil {
		t.Fatal(err)
	}
	retry("retry-first")
	b := claim("second")
	fail(b, "failed-second")
	ready := retry("retry-second-without-new-evidence")
	if _, err := f.scheduler.PrepareWorkflowAdmission(context.Background(), ready); !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("old attempt evidence authorized next failure: %v", err)
	}
}

// 删除指数退避、五分钟封顶或 jitter 上限，确定性边界断言会失败。
func TestRetryDelayIsBoundedAndDeterministic(t *testing.T) {
	for _, attempt := range []int64{1, 2, 7, 63} {
		delay := retryDelay(attempt, "attempt")
		base := 5 * time.Second
		for n := int64(1); n < attempt && base < 5*time.Minute; n++ {
			base *= 2
		}
		base = min(base, 5*time.Minute)
		if delay < base || delay > min(base+base/5, 5*time.Minute) || delay != retryDelay(attempt, "attempt") {
			t.Fatalf("attempt %d delay=%s base=%s", attempt, delay, base)
		}
	}
}

// 删除 min(maxAttempts,3) 会为显式允许五次的任务批准第四次自动重试。
func TestVerifiedRetryStopsAfterThreeAttempts(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	f.policy(domain.HarnessCodex, 10)
	w, _, err := f.workflow.Create(context.Background(), "capped-retry", workflow.CreateInput{Title: "Retry cap", Goal: "At most three attempts",
		Repositories: []workflow.RepositoryInput{{RepoKey: "repo", ProjectID: "project"}}, Tasks: []workflow.TaskInput{{ID: "capped", Title: "Capped", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, MaxAttempts: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	w, _, err = f.workflow.Activate(context.Background(), w.ID, w.Revision, "capped-activate")
	if err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= 3; number++ {
		task, _, err := f.store.GetWorkflowTask(context.Background(), w.ID, "capped")
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := f.workflow.Claim(context.Background(), workflow.ClaimInput{WorkflowID: w.ID, TaskID: task.ID, ExpectedRevision: task.Revision, WorkerID: "worker", IdempotencyKey: fmt.Sprintf("capped-claim-%d", number)})
		if err != nil || !ok {
			t.Fatalf("claim %d: %v %v", number, ok, err)
		}
		a := claimed.Attempt
		if _, _, err := f.workflow.Settle(f.permit(a.ID), workflow.SettleInput{WorkflowID: w.ID, AttemptID: a.ID, LeaseToken: a.LeaseToken, Result: domain.WorkflowAttemptStateFailed, IdempotencyKey: fmt.Sprintf("capped-fail-%d", number)}); err != nil {
			t.Fatal(err)
		}
		due, err := f.scheduler.ScheduleVerifiedRetry(context.Background(), VerifiedRetry{WorkflowID: w.ID, TaskID: task.ID, AttemptID: a.ID, ReasonCode: "SAFE_TO_RETRY"})
		if number == 3 {
			if !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
				t.Fatalf("fourth retry authorized: %+v %v", due, err)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(due.NextEligibleAt.Sub(f.clock.Now()) + time.Millisecond)
		if _, err := f.scheduler.MaintainLeadership(context.Background()); err != nil {
			t.Fatal(err)
		}
		task, _, err = f.store.GetWorkflowTask(context.Background(), w.ID, "capped")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.workflow.Retry(context.Background(), workflow.RetryInput{WorkflowID: w.ID, TaskID: task.ID, ExpectedRevision: task.Revision, IdempotencyKey: fmt.Sprintf("capped-retry-%d", number)}); err != nil {
			t.Fatal(err)
		}
	}
}
