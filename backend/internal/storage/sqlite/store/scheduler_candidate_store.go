package store

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListSchedulerWorkflows 用稳定 ID 游标扫描活跃工作流，单次最多读取 100 项。
func (s *Store) ListSchedulerWorkflows(ctx context.Context, after domain.WorkflowID, limit int64) ([]domain.Workflow, error) {
	if limit < 1 || limit > 100 {
		return nil, domain.ErrSchedulerPolicy
	}
	rows, err := s.qr.ListSchedulerWorkflows(ctx, gen.ListSchedulerWorkflowsParams{AfterID: after, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Workflow, 0, len(rows))
	for _, r := range rows {
		out = append(out, workflowFromGen(r))
	}
	return out, nil
}

// ListSchedulerCandidates 由 SQL 按优先级和有界老化排序，整数加法饱和后保持稳定次序。
// Store 已统一 UTC；SQL 取日期时间前缀，去除驱动写入但 unixepoch 不识别的 +0000 UTC 后缀。
func (s *Store) ListSchedulerCandidates(ctx context.Context, w domain.WorkflowID, now time.Time, limit, offset int64) ([]domain.WorkflowTask, error) {
	if limit < 1 || limit > 100 || offset < 0 || now.IsZero() {
		return nil, domain.ErrSchedulerPolicy
	}
	rows, err := s.qr.ListSchedulerCandidates(ctx, gen.ListSchedulerCandidatesParams{WorkflowID: w, NowEpoch: now.Unix(), RowLimit: limit, CandidateOffset: offset})
	if err != nil {
		return nil, err
	}
	out := make([]domain.WorkflowTask, 0, len(rows))
	for _, r := range rows {
		out = append(out, workflowTaskFromGen(r))
	}
	return out, nil
}
