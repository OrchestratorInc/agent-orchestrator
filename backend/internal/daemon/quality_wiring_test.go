package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// qualityPlanStore 只提供冻结计划；其他查询使用临时 SQLite，以抵达 remote 准入边界而不建立 Git 仓库。
type qualityPlanStore struct {
	*sqlite.Store
	plan domain.IntegrationPlan
}

// GetIntegrationPlan 返回已摘要绑定的计划，让默认 remote 闭锁成为 Push 的实际拒绝原因。
func (s qualityPlanStore) GetIntegrationPlan(context.Context, string) (domain.IntegrationPlan, bool, error) {
	return s.plan, true, nil
}

// TestQualityWiring 运行正式装配和审批消费；无登记策略及 remote 时不能获得写权限。
func TestQualityWiring(t *testing.T) {
	ctx := context.Background()
	store, dataDir := openDaemonTestStore(t), t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := newWorkflowOperatorAuth(dataDir, store, nil, log)
	deps := httpd.APIDeps{}
	wireQualityServices(&deps, store, dataDir, auth.Approvals, log)
	path, payload := "/api/v1/workflows/w1/integrations/p1/apply", "{}"
	digest := principal.PayloadDigest([]byte(payload))
	approval, err := auth.Approvals.CreateWithPayload("POST", path, digest, payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Approvals.VerifyAndConfirm(ctx, approval.ID, approval.Code, digest, &operatorConsentAccepted{}); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := deps.WorkflowIntegrations.Approvals.Consume(approval.ID, "POST", path, digest)
	if err != nil || fingerprint != principal.NativeConsentIdentity {
		t.Fatalf("质量装配未复用已确认审批: fingerprint=%q, err=%v", fingerprint, err)
	}
	if _, err := auth.Approvals.Consume(approval.ID, "POST", path, digest); err == nil {
		t.Fatal("质量服务消费后，主审批表仍允许重放")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalConsumed {
		t.Fatalf("审批未记录为已消费: %v", err)
	}
	caller := principal.Caller{Kind: principal.KindOperator, OperatorTokenFingerprint: fingerprint}
	for _, invalid := range []bool{false, true} {
		if invalid {
			if err := os.WriteFile(filepath.Join(dataDir, "quality-policies.json"), []byte("[{"), 0600); err != nil {
				t.Fatal(err)
			}
			wireQualityServices(&deps, store, dataDir, auth.Approvals, log)
		}
		if _, err := deps.WorkflowGates.Configure(ctx, "w1", "unregistered", 0, caller, domain.SchedulerFence{}); !errors.Is(err, domain.ErrDeliveryBlocked) {
			t.Fatalf("无可信策略仍能配置 Gate (invalid=%t): %v", invalid, err)
		}
	}
	entry := domain.IntegrationRepoPreview{PlanID: "p1", RepoKey: "repo", TargetRef: "refs/heads/main", MergeCommitSHA: "candidate"}
	plan := domain.IntegrationPlan{ID: "p1", WorkflowID: "w1", Status: "clean", Revision: 1}
	entries := []domain.IntegrationRepoPreview{entry}
	plan.PlanDigest = plan.Digest(entries)
	raw, err := json.Marshal(domain.IntegrationApplyRequest{IdempotencyKey: "push-1", ExpectedRevision: 1,
		PlanDigest: plan.PlanDigest, Entries: entries, PushTargets: []domain.IntegrationPushTarget{{
			RepoKey: entry.RepoKey, RemoteKey: "origin", RemoteIdentity: "host-remote", TargetRef: entry.TargetRef, CommitSHA: entry.MergeCommitSHA,
		}}})
	if err != nil {
		t.Fatal(err)
	}
	deps.WorkflowIntegrations.Store = qualityPlanStore{Store: store, plan: plan}
	if _, err := deps.WorkflowIntegrations.Push(ctx, "w1", plan.ID, raw, "", domain.SchedulerFence{}); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("默认 remote 未在 Git 传输前闭锁: %v", err)
	}
}
