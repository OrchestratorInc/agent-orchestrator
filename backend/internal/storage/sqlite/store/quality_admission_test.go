package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestM4QualityAdmissionAndReviewerGeneration 删除质量放行或 Session 代际检查，会放行无证据后继或旧审查者。
func TestM4QualityAdmissionAndReviewerGeneration(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	a := x.artifacts[0]
	if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	policy := domain.GatePolicy{ID: "test", Revision: 1, ValidFor: time.Hour, MaxFixes: 2, Rules: []domain.GateRule{{ID: "unit", Required: true, MaxLogBytes: 64, Level: 1}}}
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, policy, 0, "operator", x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	x.stop(t)
	// 植入 M1 成功事实，安全边界由真实 ReserveAndClaim 验证。
	if _, err := x.db.Exec("UPDATE workflow_tasks SET state='succeeded' WHERE id=?", a.TaskID); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ id, kind string }{{"reviewer", "review"}, {"next", "integration"}, {"later", "integration"}} {
		if _, err := x.db.Exec("INSERT INTO workflow_tasks(id,workflow_id,kind,title,state,created_at,updated_at) VALUES(?,?,?,?,'ready',?,?)", v.id, a.WorkflowID, v.kind, v.id, x.now, x.now); err != nil {
			t.Fatal(err)
		}
		if _, err := x.db.Exec("INSERT INTO workflow_edges(workflow_id,from_task_id,to_task_id) VALUES(?,?,?)", a.WorkflowID, a.TaskID, v.id); err != nil {
			t.Fatal(err)
		}
	}
	f := sqlitetest.NewAdmissionFixture(t, x.s, func() time.Time { return x.now })
	claim := func(id string) (domain.WorkflowAttempt, error) {
		attempt := workflowStoreAttempt(domain.WorkflowAttemptID(id+"-attempt"), a.WorkflowID, domain.WorkflowTaskID(id), "worker", "lease", x.now, x.now.Add(20*time.Second))
		got, _, err := f.Claim(ctx, attempt, 1, "claim-"+id, strings.Repeat("f", 64))
		return got, err
	}
	if _, err := claim("next"); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("M1 bypassed Gate: %v", err)
	}
	gate := domain.GateRun{ID: "gate", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: policy.Digest(), Fingerprint: strings.Repeat("e", 64), Fence: x.ad.Fence}
	if _, _, err := x.s.BeginGateRun(ctx, gate, x.now); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := x.s.FinishGateRun(ctx, gate.ID, 1, gate.Fingerprint, []domain.GateResult{{RuleID: "unit", State: "PASS", ExitCode: &zero, LogDigest: strings.Repeat("a", 64)}}, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err := claim("next"); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("Gate bypassed Review: %v", err)
	}
	reviewer, err := claim("reviewer")
	if err != nil {
		t.Fatalf("review task must start after Gate without waiting for its own review: %v", err)
	}
	permitCtx := func() context.Context { return sqlitetest.ExecutionContext(ctx, t, x.s, reviewer.ID, x.now) }
	b := domain.WorkflowAttemptBinding{AttemptID: reviewer.ID, WorkflowID: a.WorkflowID, TaskID: reviewer.TaskID, SpawnRequestID: "review-spawn", SpawnHash: strings.Repeat("a", 64)}
	b, _, err = x.s.CreateWorkflowAttemptBinding(permitCtx(), b, nil, x.now)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err = x.s.TransitionWorkflowAttemptBinding(permitCtx(), b.AttemptID, domain.WorkflowBindingStateSpawnRequested, "", b.Revision, x.now)
	if err != nil {
		t.Fatal(err)
	}
	ad, _, err := x.s.GetSchedulerAdmissionForAttempt(ctx, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := x.seed
	rec.ClientRequestID, rec.ClientRequestHash = b.SpawnRequestID, b.SpawnHash
	rec.Metadata.ControllerGeneration = "review-generation"
	session, _, err := x.s.CreateAdmittedSession(permitCtx(), rec, ad, "", x.now)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err = x.s.GetWorkflowAttemptBinding(ctx, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.s.BindWorkflowAttemptSession(permitCtx(), reviewer.ID, session.ID, "review-generation", domain.WorkflowBindingStateBoundReady, b.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	actor := domain.QualityReviewActor{AttemptID: reviewer.ID, SessionID: session.ID, Generation: "review-generation"}
	r := domain.QualityReview{ID: "independent", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyRevision: 1, PolicyDigest: policy.Digest(), TargetSHA: a.Repo.BaseSHA, Actor: actor, Fence: x.ad.Fence}
	if _, err = x.s.BeginQualityReview(ctx, r, x.now); err != nil {
		t.Fatal(err)
	}
	session.Metadata.ControllerGeneration = "replacement-generation"
	if err = x.s.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.FinishQualityReview(ctx, r.ID, 1, actor, domain.VerdictApproved, nil, 2, x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("old reviewer generation approved: %v", err)
	}
	session.Metadata.ControllerGeneration = actor.Generation
	if err = x.s.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	stale := x.ad.Fence
	stale.Epoch++
	if _, err = x.s.FinishQualityReview(ctx, r.ID, 1, actor, domain.VerdictApproved, nil, 2, stale, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("wrong epoch approved: %v", err)
	}
	if _, err = x.s.FinishQualityReview(ctx, r.ID, 1, actor, domain.VerdictApproved, nil, 2, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err = claim("next"); err != nil {
		t.Fatalf("verified predecessor refused: %v", err)
	}
	r.ID = "new-review"
	if _, err = x.s.BeginQualityReview(ctx, r, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err = claim("later"); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("old approved bypassed new review: %v", err)
	}
}
