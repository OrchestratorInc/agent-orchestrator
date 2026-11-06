package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestM4GateEvidenceCASAndPolicyFencing 删除结果真实性、revision 或策略指纹检查会放行伪 PASS/过期回执。
func TestM4GateEvidenceCASAndPolicyFencing(t *testing.T) {
	x := newQualityFixture(t, 1)
	a := x.artifacts[0]
	ctx := context.Background()
	if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	a.Git.Files = []domain.ArtifactFile{{Path: "src/check.go", Mode: "100644", BlobSHA: strings.Repeat("a", 40)}}
	a.ID = "with-change"
	a.ArtifactRevision++
	a.Fingerprint = a.Digest()
	if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	p := domain.GatePolicy{ID: "trusted", Revision: 1, ValidFor: time.Hour, MaxFixes: 2, Rules: []domain.GateRule{{ID: "unit", Required: true, MaxLogBytes: 64, Level: 3, WhenPaths: []string{"./src/"}}}}
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, p, 0, "test-operator", x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	r := domain.GateRun{ID: "gate", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: p.Digest(), Fingerprint: strings.Repeat("e", 64), Fence: x.ad.Fence}
	started, made, err := x.s.BeginGateRun(ctx, r, x.now)
	if err != nil || !made {
		t.Fatalf("begin=%+v %v", started, err)
	}
	if _, made, err := x.s.BeginGateRun(ctx, r, x.now); err != nil || made {
		t.Fatalf("duplicate execution intent=%v %v", made, err)
	}
	concurrent := r
	concurrent.ID = "another-run"
	if _, _, err := x.s.BeginGateRun(ctx, concurrent, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("new ID bypassed unresolved run: %v", err)
	}
	fake := []domain.GateResult{{RuleID: "unit", State: "PASS", LogDigest: strings.Repeat("a", 64)}}
	if _, err := x.s.FinishGateRun(ctx, r.ID, 1, r.Fingerprint, []domain.GateResult{{RuleID: "unit", State: "SKIPPED", Reason: "paths_not_changed"}}, x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("required changed scope skipped: %v", err)
	}
	if _, err := x.s.FinishGateRun(ctx, r.ID, 1, r.Fingerprint, fake, x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("PASS without exit=%v", err)
	}
	zero := 0
	fake[0].ExitCode = &zero
	result, err := x.s.FinishGateRun(ctx, r.ID, 1, r.Fingerprint, fake, x.ad.Fence, x.now)
	if err != nil || result.State != "PASS" {
		t.Fatalf("finish=%+v %v", result, err)
	}
	if _, err := x.s.FinishGateRun(ctx, r.ID, 1, r.Fingerprint, fake, x.ad.Fence, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("stale CAS=%v", err)
	}
	r.ID = "old-policy"
	if _, _, err := x.s.BeginGateRun(ctx, r, x.now); err != nil {
		t.Fatal(err)
	}
	p.Revision++
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, p, 1, "test-operator", x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.FinishGateRun(ctx, r.ID, 1, r.Fingerprint, fake, x.ad.Fence, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("old policy=%v", err)
	}
}

// TestM4GateConcurrentIntent 删除 writer 内跨 RunID 闭锁，会使两个独立连接同时取得同一工件的执行权。
func TestM4GateConcurrentIntent(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	a := x.artifacts[0]
	if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	p := domain.GatePolicy{ID: "trusted", Revision: 1, ValidFor: time.Hour, MaxFixes: 2, Rules: []domain.GateRule{{ID: "unit", Required: true}}}
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, p, 0, "test-operator", x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start, done := make(chan struct{}), make(chan error, 2)
	for i, s := range []*sqlite.Store{x.s, second} {
		go func() {
			<-start
			_, created, err := s.BeginGateRun(ctx, domain.GateRun{ID: []string{"one", "two"}[i], ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: p.Digest(), Fingerprint: strings.Repeat("e", 64), Fence: x.ad.Fence}, x.now)
			if err == nil && !created {
				err = domain.ErrArtifactInvalid
			}
			done <- err
		}()
	}
	close(start)
	passed, blocked := 0, 0
	for range 2 {
		err := <-done
		if err == nil {
			passed++
		} else if errors.Is(err, domain.ErrDeliveryBlocked) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || blocked != 1 {
		t.Fatalf("concurrent results: passed=%d blocked=%d", passed, blocked)
	}
}
