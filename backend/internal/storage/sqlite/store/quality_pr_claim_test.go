package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	prservice "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

// TestM4WorkflowPROwnershipCannotBeTransferred 删除事务内 binding 检查会允许普通 Worker 洗掉 Workflow 归属。
func TestM4WorkflowPROwnershipCannotBeTransferred(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	ordinary, err := x.s.CreateSession(ctx, sampleRecord(string(x.rec.ProjectID)))
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/example/offline/pull/42", Number: 42, SessionID: x.rec.ID, UpdatedAt: x.now}
	if _, err := x.s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatal(err)
	}
	for _, terminated := range []bool{false, true} {
		if _, err := x.db.Exec(`UPDATE sessions SET is_terminated=? WHERE id=?`, terminated, x.rec.ID); err != nil {
			t.Fatal(err)
		}
		pr.SessionID = ordinary.ID
		if _, err := x.s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); !errors.Is(err, domain.ErrDeliveryBlocked) {
			t.Fatalf("workflow PR takeover terminated=%v: %v", terminated, err)
		}
		got, found, err := x.s.GetPR(ctx, pr.URL)
		if err != nil || !found || got.SessionID != x.rec.ID {
			t.Fatalf("ownership lost: %+v found=%v err=%v", got, found, err)
		}
		pr.SessionID = x.rec.ID
		if _, err := x.s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
			t.Fatalf("same-owner refresh rejected: %v", err)
		}
	}
	// 创建已接受、Bind 回执尚未到达时，Admission/Spawn 身份仍必须闭锁普通 Merge。
	x = newQualityAdmittedFixture(t, 1)
	binding, found, err := x.s.GetWorkflowAttemptBinding(ctx, x.attempt.ID)
	if err != nil || !found || binding.SessionID != nil || binding.State != domain.WorkflowBindingStateSpawnRequested {
		t.Fatalf("not a legal pre-Bind state: %+v %v", binding, err)
	}
	ordinary, err = x.s.CreateSession(ctx, sampleRecord(string(x.rec.ProjectID)))
	if err != nil {
		t.Fatal(err)
	}
	pr.SessionID = x.rec.ID
	if _, err := x.s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatal(err)
	}
	if owned, err := x.s.HasWorkflowSessionOwnership(ctx, x.rec.ID); err != nil || !owned {
		t.Fatalf("accepted spawn lost ownership before Bind: %v %v", owned, err)
	}
	pr.SessionID = ordinary.ID
	if _, err := x.s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("pre-Bind workflow takeover: %v", err)
	}
}

// claimDuringMergeSCM 在外部 readiness 观察窗口执行真实 Claim；只模拟 SCM，无网络副作用。
type claimDuringMergeSCM struct {
	pr     domain.PullRequest
	claim  func()
	merges int
}

func (s *claimDuringMergeSCM) FetchPullRequests(context.Context, []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	s.claim()
	return []ports.SCMObservation{{Fetched: true, PR: ports.SCMPRObservation{URL: s.pr.URL, Number: s.pr.Number, HeadSHA: s.pr.HeadSHA},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: s.pr.HeadSHA},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true}}}, nil
}

func (s *claimDuringMergeSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return ports.SCMReviewObservation{}, nil
}

func (s *claimDuringMergeSCM) MergePullRequest(context.Context, ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	s.merges++
	return ports.SCMMergeResult{MergeCommitSHA: strings.Repeat("b", 40)}, nil
}

// TestM4WorkflowClaimDuringOrdinaryMerge 删除目标 Workflow 归属检查会在初次 Merge 检查后将 PR 转入 Workflow。
func TestM4WorkflowClaimDuringOrdinaryMerge(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	ordinary, err := x.s.CreateSession(ctx, sampleRecord(string(x.rec.ProjectID)))
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/example/offline/pull/43", Number: 43, SessionID: ordinary.ID, UpdatedAt: x.now,
		Provider: "github", Host: "github.com", Repo: "example/offline", HeadSHA: strings.Repeat("a", 40), Mergeability: domain.MergeMergeable}
	if _, err := x.s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatal(err)
	}
	scm := &claimDuringMergeSCM{pr: pr, claim: func() {
		target := pr
		target.SessionID = x.rec.ID
		if _, err := x.s.ClaimPR(ctx, target, nil, nil, nil, nil, ports.ReviewWritePreserve, true); !errors.Is(err, domain.ErrDeliveryBlocked) {
			t.Errorf("ordinary PR entered Workflow during merge: %v", err)
		}
	}}
	svc := prservice.NewActionService(prservice.ActionDeps{Store: x.s, Reader: scm, Merger: scm})
	if _, err := svc.Merge(ctx, prservice.MergeRequest{PRID: "43", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA}); err != nil || scm.merges != 1 {
		t.Fatalf("ordinary merge regression: merges=%d err=%v", scm.merges, err)
	}
	got, _, err := x.s.GetPR(ctx, pr.URL)
	if err != nil || got.SessionID != ordinary.ID {
		t.Fatalf("merged PR became Workflow-owned: %+v err=%v", got, err)
	}
}
