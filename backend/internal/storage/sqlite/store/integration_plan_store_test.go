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
)

// TestM4IntegrationPlanAtomicReplay 删除完整集合、事务或摘要检查，会放行缺仓、遗留半条计划或接受冲突重放。
func TestM4IntegrationPlanAtomicReplay(t *testing.T) {
	x, plan, entries := newIntegrationPlanFixture(t)
	ctx := context.Background()
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ArtifactID)
	}
	checkIntegrationPlanAtomicReplay(t, x, plan, entries, ids, ctx)
}

// newIntegrationPlanFixture 复用真实准入、质量和任务提交链，提供尚未保存的完整计划。
func newIntegrationPlanFixture(t *testing.T) (*qualityFixture, domain.IntegrationPlan, []domain.IntegrationRepoPreview) {
	t.Helper()
	x := newQualityFixture(t, 2)
	ctx := context.Background()
	p := domain.GatePolicy{ID: "integration", Revision: 1, ValidFor: time.Hour, MaxFixes: 2, Rules: []domain.GateRule{{ID: "unit", Required: true, Level: 1, MaxLogBytes: 64}}}
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, x.artifacts[0].WorkflowID, p, 0, "operator", x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	plan := domain.IntegrationPlan{ID: "plan", WorkflowID: x.artifacts[0].WorkflowID, Status: "clean", Revision: 1, CreatedAt: x.now}
	var entries []domain.IntegrationRepoPreview
	var ids []string
	for i, a := range x.artifacts {
		if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
			t.Fatal(err)
		}
		g := domain.GateRun{ID: fmt.Sprintf("gate-%d", i), ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: p.Digest(), Fingerprint: strings.Repeat("c", 64), Fence: x.ad.Fence}
		if _, _, err := x.s.BeginGateRun(ctx, g, x.now); err != nil {
			t.Fatal(err)
		}
		zero := 0
		if _, err := x.s.FinishGateRun(ctx, g.ID, 1, g.Fingerprint, []domain.GateResult{{RuleID: "unit", State: "PASS", ExitCode: &zero, LogDigest: strings.Repeat("d", 64)}}, x.ad.Fence, x.now); err != nil {
			t.Fatal(err)
		}
		r := domain.QualityReview{ID: fmt.Sprintf("review-%d", i), ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyRevision: 1, PolicyDigest: p.Digest(), TargetSHA: a.Repo.BaseSHA, Actor: domain.QualityReviewActor{OperatorRef: "operator"}, Fence: x.ad.Fence}
		if _, err := x.s.BeginQualityReview(ctx, r, x.now); err != nil {
			t.Fatal(err)
		}
		if _, err := x.s.FinishQualityReview(ctx, r.ID, 1, r.Actor, domain.VerdictApproved, nil, 2, x.ad.Fence, x.now); err != nil {
			t.Fatal(err)
		}
		c := domain.TaskCommit{ID: fmt.Sprintf("commit-%d", i), ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, ArtifactRevision: a.ArtifactRevision, ParentSHA: a.Git.HeadSHA, TreeSHA: a.Git.TreeSHA, CommitSHA: strings.Repeat("e", 40), TargetSHA: a.Repo.BaseSHA, Fence: x.ad.Fence}
		if _, _, err := x.s.BeginTaskCommit(ctx, c, x.now); err != nil {
			t.Fatal(err)
		}
		if _, err := x.s.FinishTaskCommit(ctx, c.ID, 1, "committed", x.ad.Fence, x.now); err != nil {
			t.Fatal(err)
		}
		e := domain.IntegrationRepoPreview{PlanID: plan.ID, RepoKey: a.Repo.RepoKey, ArtifactID: a.ID, TaskCommitID: c.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: p.Digest(), TargetRef: a.Repo.TargetRef, ExpectedTargetSHA: c.TargetSHA, TaskCommitSHA: c.CommitSHA, MergeMethod: "merge", Status: "clean", TreeSHA: a.Git.TreeSHA, MergeCommitSHA: strings.Repeat("f", 40), DiffDigest: strings.Repeat("a", 64), Revision: 1}
		e.PreviewDigest = e.Digest()
		entries = append(entries, e)
		ids = append(ids, a.ID)
	}
	plan.PlanDigest = plan.Digest(entries)
	return x, plan, entries
}

// checkIntegrationPlanAtomicReplay 在两个独立连接间验证完整集合、失败回滚及单次 CDC。
func checkIntegrationPlanAtomicReplay(t *testing.T, x *qualityFixture, plan domain.IntegrationPlan, entries []domain.IntegrationRepoPreview, ids []string, ctx context.Context) {
	t.Helper()
	if err := x.s.ValidateIntegrationArtifacts(ctx, plan.WorkflowID, ids[:1], x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("missing repo=%v", err)
	}
	plan.PlanDigest = plan.Digest(entries)
	bad := append([]domain.IntegrationRepoPreview(nil), entries...)
	bad[1].TaskCommitID = "absent"
	bad[1].PreviewDigest = bad[1].Digest()
	badPlan := plan
	badPlan.PlanDigest = badPlan.Digest(bad)
	if _, err := x.s.CreateIntegrationPlan(ctx, badPlan, bad, x.ad.Fence, x.now); err == nil {
		t.Fatal("bad second repo accepted")
	}
	if _, found, err := x.s.GetIntegrationPlan(ctx, plan.ID); err != nil || found {
		t.Fatalf("transaction leaked partial plan: %v %v", found, err)
	}
	other, err := sqlite.Open(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, s := range []*sqlite.Store{x.s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = s.CreateIntegrationPlan(ctx, plan, entries, x.ad.Fence, x.now)
		}()
	}
	wg.Wait()
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := other.ListIntegrationRepoPreviews(ctx, plan.ID)
	if err != nil || len(loaded) != 2 || plan.Digest(loaded) != plan.PlanDigest {
		t.Fatalf("persisted=%+v %v", loaded, err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM change_log WHERE json_extract(payload,'$.kind')='integration_plan'`); n != 1 {
		t.Fatalf("CDC emitted %d events", n)
	}
}
