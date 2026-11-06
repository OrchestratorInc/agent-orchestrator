package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM4TaskCommitEvidenceAndRecovery 删除 Gate/Review/CAS、冻结或新任期对账边界，会授予无证据或重复 Git 写入。
func TestM4TaskCommitEvidenceAndRecovery(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	a := x.artifacts[0]
	if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	p := domain.GatePolicy{ID: "trusted", Revision: 1, ValidFor: time.Hour, MaxFixes: 2, Rules: []domain.GateRule{{ID: "unit", Required: true, Level: 1, MaxLogBytes: 64}}}
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, p, 0, "operator", x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	c := domain.TaskCommit{ID: "commit", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, ArtifactRevision: a.ArtifactRevision, ParentSHA: a.Git.HeadSHA, TreeSHA: a.Git.TreeSHA, CommitSHA: strings.Repeat("e", 40), TargetSHA: a.Repo.BaseSHA, Fence: x.ad.Fence}
	if _, _, err := x.s.BeginTaskCommit(ctx, c, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("missing Gate=%v", err)
	}
	g := domain.GateRun{ID: "gate", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: p.Digest(), Fingerprint: strings.Repeat("a", 64), Fence: x.ad.Fence}
	if _, _, err := x.s.BeginGateRun(ctx, g, x.now); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := x.s.FinishGateRun(ctx, g.ID, 1, g.Fingerprint, []domain.GateResult{{RuleID: "unit", State: "PASS", ExitCode: &zero, LogDigest: strings.Repeat("b", 64)}}, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.BeginTaskCommit(ctx, c, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("missing Review=%v", err)
	}
	r := domain.QualityReview{ID: "review", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyRevision: 1, PolicyDigest: p.Digest(), TargetSHA: a.Repo.BaseSHA, Actor: domain.QualityReviewActor{OperatorRef: "operator"}, Fence: x.ad.Fence}
	late := r
	late.ID = "late-review"
	if _, err := x.s.BeginQualityReview(ctx, late, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.BeginQualityReview(ctx, r, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.FinishQualityReview(ctx, r.ID, 1, r.Actor, domain.VerdictApproved, nil, 2, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if err := x.s.ValidateTaskCommit(ctx, c, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("validation created intent: %v", err)
	}
	if _, found, err := x.s.GetTaskCommit(ctx, c.ID); err != nil || found {
		t.Fatalf("validation side effect: found=%v err=%v", found, err)
	}
	started, made, err := x.s.BeginTaskCommit(ctx, c, x.now)
	if err != nil || !made || started.GateID != g.ID || started.ReviewID != r.ID || started.PolicyDigest != p.Digest() {
		t.Fatalf("intent=%+v %v", started, err)
	}
	if err := x.s.ValidateTaskCommit(ctx, started, x.now); err != nil {
		t.Fatal(err)
	}
	changedIntent := started
	changedIntent.TargetSHA = strings.Repeat("f", 40)
	if err := x.s.ValidateTaskCommit(ctx, changedIntent, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("changed target accepted: %v", err)
	}
	if _, err := x.s.FinishQualityReview(ctx, late.ID, 1, late.Actor, domain.VerdictChangesRequested, nil, 2, x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("late review changed evidence during Git: %v", err)
	}
	if _, made, err := x.s.BeginTaskCommit(ctx, c, x.now); err != nil || made {
		t.Fatalf("duplicate=%v %v", made, err)
	}
	c.ID = "another-key"
	if _, _, err := x.s.BeginTaskCommit(ctx, c, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("new key=%v", err)
	}
	p.Revision++
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, p, 1, "operator", x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("policy changed during Git=%v", err)
	}
	if _, _, err := x.s.TransitionArtifact(ctx, a.ID, 1, a.Fingerprint, "verified", "stale", x.ad.Fence, x.now); err == nil {
		t.Fatal("artifact revoked during Git")
	}
	future := x.now.Add(time.Minute)
	leader, changed, err := x.s.AcquireSchedulerLeader(ctx, "restarted", future, future.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("leader=%+v %v", leader, err)
	}
	leader, _, err = x.s.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.FinishTaskCommit(ctx, started.ID, 1, "committed", x.ad.Fence, future); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch=%v", err)
	}
	if err := x.s.WithTaskCommitWrite(ctx, started, func() time.Time { return future }, func(context.Context) error {
		t.Fatal("old epoch reached Git write")
		return nil
	}); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old writer=%v", err)
	}
	got, err := x.s.ReconcileTaskCommit(ctx, started.ID, 1, "committed", leader.Fence, future)
	if err != nil || got.State != "committed" || got.Fence != x.ad.Fence {
		t.Fatalf("recovery invented new execution: %+v %v", got, err)
	}
}
