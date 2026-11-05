package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestM2RepoIdentityGate 确保相同 SHA 不能替代仓库的授权 ProjectID。
func TestM2RepoIdentityGate(t *testing.T) {
	bindings := []domain.WorkflowAttemptRepoBinding{{RepoKey: "a", ProjectID: "authorized", ExpectedBaseSHA: "pin"}}
	rows := projectWorktrees([]domain.SessionWorktreeRecord{{RepoName: "a", ProjectID: "replacement", WorktreePath: "/workspace", CreationSHA: "pin"}})
	states, gate := repoGateDecision(bindings, rows)
	if !gate || states["a"] != domain.WorkflowRepoBindingStateMismatch {
		t.Fatalf("不同仓库被认作 verified：gate=%v states=%v", gate, states)
	}
}

// refusingWorkspaceOwner 模拟真实 Manager 拒绝已丢失或漂移的多仓库工作区。
type refusingWorkspaceOwner struct {
	fakeSpawner
	verified bool
}

// ObserveSessionWorktrees 返回不确定事实，而不是提供可恢复的 Git 证据。
func (o *refusingWorkspaceOwner) ObserveSessionWorktrees(context.Context, domain.SessionID) ([]ports.RepoWorktreeObservation, error) {
	o.verified = true
	return nil, errors.New("repository ownership changed")
}

// TestM2GatewayRechecksWorkspace 验证观察路径重新向工作区所有者求证，不能只读缓存 SHA。
func TestM2GatewayRechecksWorkspace(t *testing.T) {
	owner := &refusingWorkspaceOwner{}
	store := newFakeSessions()
	store.worktrees = []domain.SessionWorktreeRecord{{RepoName: "a"}, {RepoName: "b"}}
	gateway := NewLocalGateway(LocalGatewayDeps{Sessions: owner, Records: store, Worktrees: store})
	observation, err := gateway.observe(context.Background(), domain.SessionRecord{ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if !owner.verified || observation.Readiness != ports.SessionReadinessUnknown {
		t.Fatalf("未重新核验工作区：verified=%v readiness=%s", owner.verified, observation.Readiness)
	}
}
