package intervention

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// receiptTurnReader 复用 Chat 的持久 turn，不查询 Provider 或另建队列状态机。
type receiptTurnReader interface {
	TurnByID(context.Context, string) (domain.ConversationTurn, error)
}

// ListReceipts 从原始回执和 Chat 持久事实派生队列状态，页面恢复不能发送消息或创建会话。
func (c *Coordinator) ListReceipts(ctx context.Context, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID, attemptID domain.WorkflowAttemptID) ([]domain.WorkflowIntervention, error) {
	rows, err := c.store.ListWorkflowInterventionsForAttempt(ctx, workflowID, taskID)
	if err != nil {
		return nil, err
	}
	result := make([]domain.WorkflowIntervention, 0, len(rows))
	for _, row := range rows {
		if row.AttemptID != nil && *row.AttemptID == attemptID {
			if row.Mode == domain.WorkflowInterventionModeQueue && row.State == domain.WorkflowInterventionStateQueued {
				reader, ok := c.store.(receiptTurnReader)
				if !ok {
					return nil, domain.ErrDeliveryBlocked
				}
				turn, err := reader.TurnByID(ctx, row.ChatTurnID)
				if err != nil {
					return nil, err
				}
				fact := domain.InterventionChatFact{ChatTurnID: turn.ID, ChatTurnState: turn.State, ProviderTurnID: turn.ProviderTurnID}
				switch {
				case row.SessionID == nil || turn.HandledBySessionID != *row.SessionID:
					row.State, row.ReasonCode = domain.WorkflowInterventionStateUnknown, "queue_session_mismatch"
				case turn.State == domain.TurnStateQueued:
					// 编辑保留原 turn 和回执身份，等待同一队列真正派发。
				case turn.State == domain.TurnStateCancelled:
					row.State, row.ReasonCode = domain.WorkflowInterventionStateCancelled, "queue_cancelled"
				case fact.Licenses(domain.WorkflowInterventionStateDelivered):
					row.State, row.ReasonCode = domain.WorkflowInterventionStateDelivered, "queue_delivered"
					row.ProviderTurnID = turn.ProviderTurnID
				default:
					row.State, row.ReasonCode = domain.WorkflowInterventionStateUnknown, "queue_"+string(turn.State)
				}
			}
			result = append(result, row)
		}
	}
	return result, nil
}
