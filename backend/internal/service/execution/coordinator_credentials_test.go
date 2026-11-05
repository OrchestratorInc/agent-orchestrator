package execution

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestSpawnHashIgnoresWorkflowContext 保留重试幂等约束：恢复出的上下文不能改变请求指纹。
func TestSpawnHashIgnoresWorkflowContext(t *testing.T) {
	t.Parallel()
	request := sampleSpawnRequest()
	first, err := SpawnHash(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Workflow = &ports.WorkflowSpawnContext{
		WorkflowID: request.WorkflowID, TaskID: request.TaskID, AttemptID: request.AttemptID,
	}
	second, err := SpawnHash(request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("presence of a workflow context changed the spawn hash")
	}
}

// TestBindAttemptOmitsStoredLeaseFromSpawnContext 捕获真实协调器交给 Gateway 的上下文；
// 恢复旧的租约赋值会泄露持久 token，删除身份赋值则丢失 MCP 所需标识。
func TestBindAttemptOmitsStoredLeaseFromSpawnContext(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	store.seedAttemptFor("attempt-1", "stored-lease-token")
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessReady)
	coordinator := newTestCoordinator(t, store, gateway)
	if _, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil); err != nil {
		t.Fatal(err)
	}
	spawned := gateway.LastSpawn()
	if spawned.Workflow == nil {
		t.Fatal("gateway spawn carried no workflow context")
	}
	if strings.Contains(fmt.Sprintf("%+v", spawned.Workflow), "stored-lease-token") {
		t.Fatal("gateway spawn carried the internal lease")
	}
	if spawned.Workflow.AttemptID != "attempt-1" ||
		spawned.Workflow.WorkflowID != "workflow-1" || spawned.Workflow.TaskID != "task-1" {
		t.Fatal("gateway spawn lost the non-secret workflow identity")
	}
}
