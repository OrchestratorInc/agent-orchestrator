package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualityreview"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/taskcommit"
)

// TestM4QualityHelper 在临时导出树读取真实交付文件；无 Provider 的本地进程失败不可伪装 PASS。
func TestM4QualityHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "m4-quality-helper" {
		return
	}
	data, err := os.ReadFile("delivery.txt")
	if err != nil || string(data) != "revision 2" {
		fmt.Print("missing delivery")
		os.Exit(9)
	}
	fmt.Print("verified frozen tree")
	os.Exit(0)
}

// m4CheckQuality 真实 SQLite/Manager/Git/子进程装配；删除策略审批、退出码或独立身份检查都会破坏本链。
func m4CheckQuality(t *testing.T, f *m2ffFixture, artifacts *artifact.Service, a domain.ArtifactSnapshot, fence domain.SchedulerFence) *qualitygate.Service {
	t.Helper()
	ctx := context.Background()
	p := m4OfflinePolicy(t)
	gate, commits := f.m3.quality.gates, f.m3.quality.commits
	base := "/workflows/" + string(a.WorkflowID)
	if _, err := gate.Configure(ctx, a.WorkflowID, p.ID, 0, principal.Caller{}, fence); err == nil {
		t.Fatal("unapproved policy configured")
	}
	if _, found, err := f.store.GetWorkflowQualityPolicy(ctx, a.WorkflowID); err != nil {
		t.Fatal(err)
	} else if !found {
		m4HTTP[controllers.QualityPolicyResponse](t, f, http.MethodPut, base+"/quality-policy", controllers.QualityPolicyRequest{PolicyID: p.ID})
	}
	key := func(id string) string { return id + "-" + string(a.Repo.RepoKey) }
	if _, err := commits.Commit(ctx, "unverified", a.ID, a.ArtifactRevision, fence); err == nil {
		t.Fatal("commit without Gate accepted")
	}
	run := m4CLI[controllers.QualityGateResponse](t, f, "gate", a.WorkflowID, a.ID, controllers.QualityActionRequest{IdempotencyKey: key("real-gate")})
	if run.State != "PASS" {
		t.Fatalf("gate=%+v", run)
	}
	replay, err := gate.Run(ctx, key("real-gate"), a.ID, fence)
	if err != nil || replay.Revision != run.Revision {
		t.Fatalf("gate replay=%+v %v", replay, err)
	}
	review := qualityreview.Service{Store: f.store, Artifacts: artifacts, Clock: f.m3.clock.Now}
	permit, err := f.store.LoadExecutionPermit(ctx, a.AttemptID, fence, f.m3.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	self := principal.Caller{Kind: principal.KindAgent, SessionID: a.SessionID, AttemptID: a.AttemptID, LeaseProven: true, ExecutionPermit: &permit}
	if _, err := review.Begin(ctx, "self-review", a.ID, self, fence); err == nil {
		t.Fatal("author self-review accepted")
	}
	r := m4CLI[controllers.QualityReviewResponse](t, f, "review", a.WorkflowID, a.ID, controllers.QualityActionRequest{IdempotencyKey: key("real-review")}).Review
	if _, err := commits.Commit(ctx, "unreviewed", a.ID, a.ArtifactRevision, fence); err == nil {
		t.Fatal("commit without approved Review accepted")
	}
	r = m4CLI[controllers.QualityReviewResponse](t, f, "finish-review", a.WorkflowID, r.ID, controllers.QualityReviewFinishRequest{ExpectedRevision: r.Revision, Verdict: domain.VerdictApproved, Findings: []domain.QualityFinding{}}).Review
	if r.State != "approved" {
		t.Fatalf("review=%+v", r)
	}
	if a.Repo.RepoKey == "repo-b" {
		commits.Store = &m4CommitReceiptLoss{Store: f.store}
		if _, err := commits.Commit(ctx, key("real-commit"), a.ID, a.ArtifactRevision, fence); err == nil {
			t.Fatal("lost receipt falsely succeeded")
		}
		intent, found, err := f.store.GetTaskCommit(ctx, key("real-commit"))
		if err != nil || !found || intent.State != "requested" {
			t.Fatalf("lost receipt intent=%+v %v", intent, err)
		}
	}
	commits.Store = f.store
	committed := m4CLI[controllers.QualityCommitResponse](t, f, "commit", a.WorkflowID, a.ID, controllers.QualityActionRequest{IdempotencyKey: key("real-commit"), ArtifactRevision: a.ArtifactRevision}).Commit
	if committed.State != "committed" {
		t.Fatalf("recovered commit=%+v", committed)
	}
	repeated, err := commits.Commit(ctx, key("real-commit"), a.ID, a.ArtifactRevision, fence)
	if err != nil || repeated.CommitSHA != committed.CommitSHA || repeated.Revision != committed.Revision {
		t.Fatalf("replay=%+v %v", repeated, err)
	}
	if _, err := commits.Commit(ctx, "different-key", a.ID, a.ArtifactRevision, fence); err == nil {
		t.Fatal("different key re-executed same artifact")
	}
	return gate
}

// m4CommitReceiptLoss 只注入一次数据库回执丢失，真实 Git 已执行；删除对账路径将导致重复副作用或无法恢复。
type m4CommitReceiptLoss struct{ taskcommit.Store }

func (s *m4CommitReceiptLoss) FinishTaskCommit(context.Context, string, int64, string, domain.SchedulerFence, time.Time) (domain.TaskCommit, error) {
	return domain.TaskCommit{}, errors.New("injected receipt loss after Git")
}
