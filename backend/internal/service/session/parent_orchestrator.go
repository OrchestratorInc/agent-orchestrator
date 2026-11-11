package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// parentOrchestratorReader 从持久事实批量读取 UI 归属，不改变任务汇报路由。
type parentOrchestratorReader interface {
	ListParentOrchestrators(context.Context, []domain.SessionID) (map[domain.SessionID]domain.SessionID, error)
}

// parentOrchestrators 让列表与详情共享查询；不支持持久关系的存储不推测归属。
func (s *Service) parentOrchestrators(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionID, error) {
	reader, ok := s.store.(parentOrchestratorReader)
	if !ok {
		return nil, nil
	}
	parents, err := reader.ListParentOrchestrators(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list parent orchestrators: %w", err)
	}
	return parents, nil
}
