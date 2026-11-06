package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/coordination"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// coordinationRead 走真实 Session 读模型；任何来源错误都必须由测试显式处理。
func coordinationRead(t *testing.T, svc *coordination.Service, w domain.WorkflowID) domain.CoordinationSummaryView {
	t.Helper()
	out, err := svc.Read(context.Background(), w)
	if err != nil || len(out.Tasks) != 1 {
		t.Fatalf("协调摘要：tasks=%d err=%v", len(out.Tasks), err)
	}
	return out
}

// coordinationExec 仅在临时数据库注入跨读取失配，避免以写入门禁替代读投影验证。
func coordinationExec(t *testing.T, x *qualityFixture, query string, args ...any) {
	t.Helper()
	if _, err := x.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// TestM4CoordinationProjection 删除最新 Attempt 选择、Session 字段映射或消息白名单映射会变红。
func TestM4CoordinationProjection(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	sessions := session.NewWithDeps(session.Deps{Store: x.s})
	svc := &coordination.Service{Store: x.s, Sessions: sessions, Clock: func() time.Time { return x.now }}
	if changed, err := x.s.UpdateSessionModel(ctx, x.rec.ID, "coordination-model"); err != nil || !changed {
		t.Fatalf("更新模型：changed=%v err=%v", changed, err)
	}
	coordinationExec(t, x, `UPDATE workflow_attempts SET attempt_no=2, heartbeat_at=?, lease_token=? WHERE id=?`, x.now, "private-lease-token", x.attempt.ID)
	coordinationExec(t, x, `INSERT INTO workflow_attempts(id,workflow_id,task_id,attempt_no,state,created_at,updated_at) VALUES(?,?,?,1,'failed',?,?)`, "older-attempt", x.attempt.WorkflowID, x.attempt.TaskID, x.now.Add(-time.Minute), x.now)
	capability := sampleCapabilitySnapshot(domain.HarnessCodex, x.now)
	capability.ObservedSessionID, capability.Generation = x.rec.ID, x.rec.Metadata.ControllerGeneration
	if err := x.s.UpsertSessionCapabilitySnapshot(ctx, capability); err != nil {
		t.Fatal(err)
	}
	a, found, err := x.s.GetWorkflowAttempt(ctx, x.attempt.ID)
	if err != nil || !found {
		t.Fatalf("读取 Attempt：found=%v err=%v", found, err)
	}
	m := collaborationMessage(a.WorkflowID, a, nil, "projection")
	m.Body, m.PayloadRef = "secret-message-body", "private-payload-ref"
	if _, _, err := x.s.CreateWorkflowMessage(x.context(t), m, x.now); err != nil {
		t.Fatal(err)
	}
	m, _, err = x.s.UpdateWorkflowMessageDelivery(x.context(t), a.WorkflowID, m.ID, domain.WorkflowMessageDeliveryUnknown, 1, x.now)
	if err != nil {
		t.Fatal(err)
	}
	out := coordinationRead(t, svc, a.WorkflowID)
	v := out.Tasks[0]
	wantSession, err := sessions.Get(ctx, x.rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.AttemptID != a.ID || v.AttemptNo != 2 || v.AttemptState != a.State || v.SessionID != x.rec.ID ||
		v.Model != "coordination-model" || v.SessionStatus != string(wantSession.Status) || v.SessionReadiness != wantSession.StatusReadiness || !v.LeaseValid ||
		!v.HeartbeatAt.Equal(x.now) || !v.LeaseUntil.Equal(*a.LeaseUntil) || v.AdmissionState != domain.AdmissionActive || v.AdmissionRevision != x.ad.Revision {
		t.Fatalf("最新执行投影错误：%+v", v)
	}
	if v.CapabilityReadiness != "ready" || v.CapabilitySource != string(capability.Source) || !v.CapabilityExpiresAt.Equal(capability.ExpiresAt) ||
		v.BudgetQuality != "UNKNOWN" || len(v.BudgetReservations) != 0 || !out.ObservedAt.Equal(x.now) || out.Truncated {
		t.Fatalf("能力或未知预算投影错误：%+v", v)
	}
	if len(out.Messages) != 1 || out.Messages[0].ID != m.ID || out.Messages[0].TaskID == nil || *out.Messages[0].TaskID != a.TaskID ||
		out.Messages[0].Kind != m.Kind || out.Messages[0].DeliveryState != domain.WorkflowMessageDeliveryUnknown || out.Messages[0].Revision != m.Revision || !out.Messages[0].CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("消息元数据丢失：%+v", out.Messages)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{m.Body, m.PayloadRef, m.PayloadSHA, "private-lease-token", "generation-1", "gitCommonDir", "worktreePath", "senderSessionId", "payloadRef"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("协调摘要泄漏：%s", secret)
		}
	}
	coordinationExec(t, x, `UPDATE workflow_attempts SET state='unknown', error_code='SPAWN_UNKNOWN' WHERE id=?`, a.ID)
	out = coordinationRead(t, svc, a.WorkflowID)
	v = out.Tasks[0]
	if v.AttemptState != domain.WorkflowAttemptStateUnknown || v.BlockedReason != "SPAWN_UNKNOWN" || v.LeaseValid || v.SessionStatus != string(wantSession.Status) || v.SessionReadiness != wantSession.StatusReadiness {
		t.Fatalf("UNKNOWN 被误判为终止或有效执行：%+v", v)
	}
	if len(out.Reservations) != 2 {
		t.Fatalf("UNKNOWN 丢失路径占位：%+v", out.Reservations)
	}
	for _, r := range out.Reservations {
		if r.ReleasedAt != nil || r.SessionID != x.rec.ID || r.AttemptID != a.ID {
			t.Fatalf("只读摘要释放或错配占位：%+v", r)
		}
	}
}

// TestM4CoordinationLeaseFence 每行独立破坏一个有效性条件，删除相应 LeaseValid 条件即变红。
func TestM4CoordinationLeaseFence(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"Attempt租约到期", `UPDATE workflow_attempts SET lease_until=heartbeat_at`},
		{"Leader租约到期", `UPDATE scheduler_leader SET lease_until=(SELECT heartbeat_at FROM workflow_attempts)`},
		{"旧epoch", `UPDATE scheduler_leader SET epoch=epoch+1`},
		{"Leader未恢复", `UPDATE scheduler_leader SET recovered=0`},
		{"Session旧generation", `UPDATE sessions SET controller_generation='generation-2'`},
		{"Admission旧generation", `UPDATE scheduler_admissions SET controller_generation='generation-2'`},
		{"Admission不确定", `UPDATE scheduler_admissions SET state='uncertain'`},
		{"Admission丢失Session", `UPDATE scheduler_admissions SET session_id=NULL`},
		{"Binding不确定", `UPDATE workflow_attempt_bindings SET state='recovery_uncertain'`},
		{"Session已终止", `UPDATE sessions SET is_terminated=1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newQualityFixture(t, 1)
			svc := &coordination.Service{Store: x.s, Sessions: session.NewWithDeps(session.Deps{Store: x.s}), Clock: func() time.Time { return x.now }}
			coordinationExec(t, x, `UPDATE workflow_attempts SET heartbeat_at=?`, x.now)
			coordinationExec(t, x, tc.query)
			out := coordinationRead(t, svc, x.attempt.WorkflowID)
			if out.Tasks[0].LeaseValid {
				t.Fatalf("失效执行仍显示有效租约：%+v", out.Tasks[0])
			}
			if len(out.Reservations) != 2 || out.Reservations[0].ReleasedAt != nil || out.Reservations[1].ReleasedAt != nil {
				t.Fatalf("摘要读取改变路径占位：%+v", out.Reservations)
			}
		})
	}
}

// TestM4CoordinationCapabilityFreshness 删除过期或代际判断会错误展示 ready；不穷举其他能力枚举。
func TestM4CoordinationCapabilityFreshness(t *testing.T) {
	x := newQualityFixture(t, 1)
	svc := &coordination.Service{Store: x.s, Sessions: session.NewWithDeps(session.Deps{Store: x.s}), Clock: func() time.Time { return x.now }}
	for _, tc := range []struct {
		name, generation string
		expires          time.Time
	}{
		{"能力到期", "generation-1", x.now},
		{"能力旧代际", "generation-old", x.now.Add(time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capability := sampleCapabilitySnapshot(domain.HarnessCodex, x.now.Add(-time.Minute))
			capability.ObservedSessionID, capability.Generation, capability.ExpiresAt = x.rec.ID, tc.generation, tc.expires
			if err := x.s.UpsertSessionCapabilitySnapshot(context.Background(), capability); err != nil {
				t.Fatal(err)
			}
			v := coordinationRead(t, svc, x.attempt.WorkflowID).Tasks[0]
			if v.CapabilityReadiness != "unknown" || v.CapabilitySource != string(capability.Source) || !v.CapabilityExpiresAt.Equal(tc.expires) || !v.LeaseValid {
				t.Fatalf("失效能力被升级或污染租约事实：%+v", v)
			}
		})
	}
}

// TestM4CoordinationUnknownBudget 删除 nil 金额判定会把未知预留伪装为已估算；已知零仍可估算。
func TestM4CoordinationUnknownBudget(t *testing.T) {
	x := newQualityFixture(t, 1)
	svc := &coordination.Service{Store: x.s, Sessions: session.NewWithDeps(session.Deps{Store: x.s}), Clock: func() time.Time { return x.now }}
	p := domain.BudgetPolicy{ID: "projection-budget", Scope: "global", Currency: "USD", Mode: domain.BudgetObserve, Period: domain.BudgetLifetime}
	if _, _, err := x.s.PutBudgetPolicy(context.Background(), p, 0, domain.PolicyChange{IdempotencyKey: "projection-policy", RequestFingerprint: collabSHA, ActorRef: "test", Reason: "TEST_BUDGET", Now: x.now}); err != nil {
		t.Fatal(err)
	}
	coordinationExec(t, x, `INSERT INTO budget_reservations(admission_id,attempt_id,policy_id,window,amount_nanos,basis,reference,state,revision) VALUES(?,?,?,'lifetime',NULL,'','','reserved',1)`, x.ad.ID, x.attempt.ID, p.ID)
	v := coordinationRead(t, svc, x.attempt.WorkflowID).Tasks[0]
	if v.BudgetQuality != "UNKNOWN" || len(v.BudgetReservations) != 1 || v.BudgetReservations[0].AmountNanos != nil || v.BudgetReservations[0].PolicyID != p.ID || v.BudgetReservations[0].Revision != 1 {
		t.Fatalf("未知预算伪装成零：%+v", v)
	}
	coordinationExec(t, x, `UPDATE budget_reservations SET amount_nanos=0, revision=revision+1`)
	v = coordinationRead(t, svc, x.attempt.WorkflowID).Tasks[0]
	if v.BudgetQuality != "ESTIMATED" || len(v.BudgetReservations) != 1 || v.BudgetReservations[0].AmountNanos == nil || *v.BudgetReservations[0].AmountNanos != 0 || v.BudgetReservations[0].Revision != 2 {
		t.Fatalf("已知零金额丢失：%+v", v)
	}
}

// TestM4CoordinationMissingSession 删除 NotFound 降级会击穿摘要；吞掉其他错误会掩盖真实数据库故障。
func TestM4CoordinationMissingSession(t *testing.T) {
	x := newQualityFixture(t, 1)
	// 第二份真实 Store 模拟读取 Attempt 后 Session 消失，避免破坏外键或另造 mock。
	sessionStore := newTestStore(t)
	svc := &coordination.Service{Store: x.s, Sessions: session.NewWithDeps(session.Deps{Store: sessionStore}), Clock: func() time.Time { return x.now }}
	t.Run("缺失保留未知", func(t *testing.T) {
		out := coordinationRead(t, svc, x.attempt.WorkflowID)
		v := out.Tasks[0]
		if v.SessionID != x.rec.ID || v.SessionStatus != "unknown" || v.SessionReadiness != "unavailable" || v.Model != "" || v.LeaseValid || v.AttemptState != x.attempt.State || v.CapabilityReadiness != "unknown" || len(out.Reservations) != 2 {
			t.Fatalf("缺失 Session 被当作终止或丢失执行事实：%+v", out)
		}
	})
	if err := sessionStore.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("数据库故障上抛", func(t *testing.T) {
		if _, err := svc.Read(context.Background(), x.attempt.WorkflowID); err == nil || !strings.Contains(err.Error(), "database is closed") {
			t.Fatalf("数据库故障被吞：%v", err)
		}
	})
}
