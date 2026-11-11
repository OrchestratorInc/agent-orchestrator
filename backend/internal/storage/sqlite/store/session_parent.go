package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ListParentOrchestrators 批量追溯确凿创建链，缺失或矛盾的父级不进入视图。
// 优先使用实际创建者，旧记录再用独立绑定和事件；无效创建父级不回退猜测。
// 查询读取事件中的创建者而非对话当前绑定，并以 UNION 去重终止循环创建链。
func (s *Store) ListParentOrchestrators(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionID, error) {
	out := make(map[domain.SessionID]domain.SessionID)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListParentOrchestrators(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("query parent orchestrators: %w", err)
	}
	for _, row := range rows {
		out[domain.SessionID(row.ChildID)] = domain.SessionID(row.ParentID)
	}
	return out, nil
}
