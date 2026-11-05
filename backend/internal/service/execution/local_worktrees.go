package execution

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sessionWorkflowReader 在实时验证能力缺失时区分无 Workflow 归属与无法确定归属；
// Spawn 尚未写入 SessionID 时仍须读取已持久化的创建意图。
type sessionWorkflowReader interface {
	GetWorkflowAttemptBindingBySession(context.Context, domain.SessionID) (domain.WorkflowAttemptBinding, bool, error)
	GetWorkflowAttemptBindingBySpawnRequest(context.Context, string) (domain.WorkflowAttemptBinding, bool, error)
}

// observeWorktrees 每次向工作区所有者求证。只有确认无显式 Repo 和 Workflow
// 绑定的 Legacy 会话可以沿用旧路径；缺少读取能力不是无绑定的证据。
func (g *LocalGateway) observeWorktrees(ctx context.Context, rec domain.SessionRecord) ([]ports.RepoWorktreeObservation, error) {
	if observer, ok := g.sessions.(SessionWorktreeObserver); ok {
		return observer.ObserveSessionWorktrees(ctx, rec.ID)
	}
	if g.worktrees == nil {
		return nil, fmt.Errorf("workspace ownership reader unavailable")
	}
	rows, err := g.worktrees.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	references := projectWorktrees(rows)
	for _, row := range rows {
		if row.ProjectID != "" || row.RepoPath != "" {
			return references, fmt.Errorf("explicit repository live verifier unavailable")
		}
	}
	reader, ok := g.records.(sessionWorkflowReader)
	if !ok {
		return references, fmt.Errorf("workflow ownership reader unavailable")
	}
	_, found, err := reader.GetWorkflowAttemptBindingBySession(ctx, rec.ID)
	if err == nil && !found && rec.ClientRequestID != "" {
		_, found, err = reader.GetWorkflowAttemptBindingBySpawnRequest(ctx, rec.ClientRequestID)
	}
	if err != nil {
		return references, err
	}
	if found {
		return references, fmt.Errorf("workflow live verifier unavailable")
	}
	return references, nil
}
