package budget

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Policies 列出所有政策用于按 HTTP 固定作用域投影，不将三级预算叠加为总费用。
func (s *Service) Policies(ctx context.Context) ([]domain.BudgetPolicy, error) {
	return s.store.ListBudgetPolicies(ctx)
}

// Snapshot 保留未知余额和对账状态，不因只读请求触发定价或执行。
func (s *Service) Snapshot(ctx context.Context, id string) (domain.BudgetSnapshot, error) {
	return s.store.ReadBudgetSnapshot(ctx, id, s.now().UTC())
}

// PutPolicy 再检查本次独立 Operator 身份，预算修改不能信任 Agent 提交的主体或时间。
func (s *Service) PutPolicy(ctx context.Context, p domain.BudgetPolicy, expected int64, change domain.PolicyChange, caller principal.Caller) (domain.BudgetPolicy, bool, error) {
	if !caller.IsOperator() || caller.IsAgent() || caller.LeaseProven || caller.AttemptID != "" || caller.SessionID != "" || strings.TrimSpace(caller.OperatorTokenFingerprint) == "" {
		return domain.BudgetPolicy{}, false, principal.ErrNotAuthorized
	}
	change.ActorRef, change.Now = caller.OperatorTokenFingerprint, s.now().UTC()
	return s.store.PutBudgetPolicy(ctx, p, expected, change)
}

// Usage 公开有界的 canonical 投影，不允许控制面写入费用事实。
func (s *Service) Usage(ctx context.Context, scope, scopeID string, after, limit int64) ([]domain.BudgetUsageEntry, error) {
	return s.store.ListBudgetUsagePage(ctx, scope, scopeID, after, limit)
}
