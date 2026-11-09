package store_test

import (
	"context"
	"encoding/json"
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

// 删除 Store 的容量判断会使此测试批准第四个/第十一个执行；删掉 Adapter 检查会批准第三个。
func TestM3AdmissionCapacityAndSanitizedCDC(t *testing.T) {
	for _, cap := range []int64{3, 10} {
		t.Run(fmt.Sprint(cap), func(t *testing.T) {
			s := newTestStore(t)
			now := workflowStoreTime()
			f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
			m3Capacity(t, s, "", cap, now)
			m3Capacity(t, s, domain.HarnessCodex, 10, now)
			for i := int64(0); i <= cap; i++ {
				in := m3ClaimInput(t, s, f, fmt.Sprintf("cap-%d", i), now)
				_, _, created, err := s.ReserveAndClaim(context.Background(), in)
				if i < cap && (err != nil || !created) {
					t.Fatalf("claim %d: %v %v", i, created, err)
				}
				if i == cap && (!errors.Is(err, domain.ErrSchedulerCapacity) || created) {
					t.Fatalf("overflow: %v %v", created, err)
				}
			}
			events, err := s.EventsAfter(context.Background(), 0, 1000)
			if err != nil {
				t.Fatal(err)
			}
			seen := 0
			for _, e := range events {
				if string(e.Type) != "scheduler_updated" {
					continue
				}
				seen++
				var p map[string]any
				if err := json.Unmarshal(e.Payload, &p); err != nil {
					t.Fatal(err)
				}
				for k := range p {
					if k != "kind" && k != "id" && k != "state" && k != "revision" && k != "epoch" {
						t.Fatalf("unexpected CDC field %q", k)
					}
				}
			}
			if seen == 0 {
				t.Fatal("no scheduler CDC")
			}
		})
	}
	s := newTestStore(t)
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		in := m3ClaimInput(t, s, f, fmt.Sprintf("adapter-%d", i), now)
		_, _, created, err := s.ReserveAndClaim(context.Background(), in)
		if i < 2 && (err != nil || !created) {
			t.Fatal(err)
		}
		if i == 2 && !errors.Is(err, domain.ErrSchedulerCapacity) {
			t.Fatalf("adapter exceeded: %v", err)
		}
	}
}

// 删除事务内幂等查找/指纹比较或原子 CAS 会让双 Store 重复预留或接受不同请求。
func TestM3AdmissionIndependentStoresReplayAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first := sqlitetest.MustOpenAt(t, dir)
	second, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, first, func() time.Time { return now })
	in := m3ClaimInput(t, first, f, "race", now)
	var wg sync.WaitGroup
	var got [2]domain.WorkflowAttempt
	var created [2]bool
	var errs [2]error
	for i, s := range []*sqlite.Store{first, second} {
		wg.Add(1)
		go func(i int, s *sqlite.Store) {
			defer wg.Done()
			candidate := in
			candidate.Attempt.ID = domain.WorkflowAttemptID(fmt.Sprintf("race-attempt-%d", i))
			got[i], _, created[i], errs[i] = s.ReserveAndClaim(ctx, candidate)
		}(i, s)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || created[0] == created[1] || got[0].ID != got[1].ID {
		t.Fatalf("race: %+v %+v %+v", got, created, errs)
	}
	in.RequestFingerprint = strings.Repeat("d", 64)
	if _, _, _, err := second.ReserveAndClaim(ctx, in); !errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		t.Fatalf("fingerprint reuse: %v", err)
	}
	rollback := m3ClaimInput(t, first, f, "rollback", now)
	rollback.ExpectedTaskRevision++
	if _, _, _, err := first.ReserveAndClaim(ctx, rollback); !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("CAS: %v", err)
	}
	ads, err := first.ListSchedulerAdmissions(ctx, false)
	if err != nil || len(ads) != 1 {
		t.Fatalf("partial admission: %d %v", len(ads), err)
	}
	decisions, err := first.ListSchedulerDecisions(ctx, 0, 100)
	if err != nil || len(decisions) != 1 {
		t.Fatalf("rolled-back success audit: %d %v", len(decisions), err)
	}
	if _, _, err := first.ClaimWorkflowTask(ctx, rollback.Attempt, rollback.ExpectedTaskRevision, "legacy", strings.Repeat("e", 64)); !errors.Is(err, domain.ErrAdmissionRequired) {
		t.Fatalf("legacy bypass: %v", err)
	}
}

// 删除 epoch/recovered 校验或失效资源检查会让旧 Leader、首样和 UNKNOWN 获取新占位。
func TestM3AdmissionFenceRecoveryAndUnknownResources(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	in := m3ClaimInput(t, s, f, "fence", now)
	l, won, err := s.AcquireSchedulerLeader(ctx, "next", now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || !won {
		t.Fatalf("takeover: %v %v", won, err)
	}
	if _, _, _, err := s.ReserveAndClaim(ctx, in); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch: %v", err)
	}
	in.Evidence.Fence = l.Fence
	in.Evidence.Now = now.Add(time.Minute)
	if _, _, _, err := s.ReserveAndClaim(ctx, in); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("unrecovered: %v", err)
	}
	_, _, err = s.CompleteSchedulerRecovery(ctx, l.Fence, l.Revision, in.Evidence.Now)
	if err != nil {
		t.Fatal(err)
	}
	in.Evidence.Capability.ObservedAt = in.Evidence.Now
	in.Evidence.Capability.ExpiresAt = in.Evidence.Now.Add(time.Minute)
	in.Evidence.Resources.ObservedAt = in.Evidence.Now
	in.Evidence.Resources.PreviousAt = in.Evidence.Now.Add(-5 * time.Second)
	in.Evidence.Resources.ReasonCode = "CPU_FIRST_SAMPLE"
	if _, _, _, err := s.ReserveAndClaim(ctx, in); err == nil {
		t.Fatal("unknown resources admitted")
	}
	ads, _ := s.ListSchedulerAdmissions(ctx, false)
	if len(ads) != 0 {
		t.Fatal("rejected admission persisted")
	}
}

// m3ClaimInput 创建独立真实 DAG，并仅通过显式可信 Fixture 取得证据。
func m3ClaimInput(t *testing.T, s *sqlite.Store, f *sqlitetest.AdmissionFixture, id string, now time.Time) domain.ClaimAdmissionInput {
	t.Helper()
	ctx := context.Background()
	w := domain.WorkflowID(id)
	taskID := domain.WorkflowTaskID(id + "-task")
	createWorkflowForStoreTest(t, s, w, []domain.WorkflowTask{workflowStoreTask(taskID, w, 2, now)}, nil, "create-"+id, strings.Repeat("a", 64), now)
	wf, _, err := s.ActivateWorkflow(ctx, w, 1, "activate-"+id, strings.Repeat("b", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.GetWorkflowTask(ctx, w, taskID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.PrepareWorkflowAdmission(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ClaimAdmissionInput{Attempt: workflowStoreAttempt(domain.WorkflowAttemptID(id+"-attempt"), w, taskID, "worker", "secret-lease", now, now.Add(15*time.Minute)), ExpectedTaskRevision: task.Revision, ExpectedWorkflowRevision: wf.Revision, IdempotencyKey: "claim-" + id, RequestFingerprint: strings.Repeat("c", 64), Evidence: e}
}

func m3Capacity(t *testing.T, s *sqlite.Store, h domain.AgentHarness, n int64, now time.Time) {
	t.Helper()
	p := domain.DefaultSchedulerPolicy(h)
	expected := p.Revision
	p.MaxConcurrent = n
	if _, _, err := s.PutSchedulerPolicy(context.Background(), p, expected, domain.PolicyChange{IdempotencyKey: "capacity-" + p.ID, RequestFingerprint: strings.Repeat("c", 64), ActorRef: "sqlitetest", Reason: "TEST_CAPACITY", Now: now}); err != nil {
		t.Fatal(err)
	}
}
