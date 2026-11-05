package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 删除派生树限额会使没有 dependsOn 边的申请绕过原来的深度或分支边界。
func TestM3TaskMaterializationBoundsLineageWithoutDependencyEdges(t *testing.T) {
	ctx := context.Background()
	s, dir := materializationStoreFixture(t)
	limits := domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 2, MaxFanOut: 1}
	in := approveMaterialization(t, s, dir, "bounded-child", "materialization-parent", materializationPayload)
	child, _, err := s.MaterializeWorkflowTaskRequest(ctx, in, limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		id     string
		parent domain.WorkflowTaskID
	}{{"extra-sibling", "materialization-parent"}, {"too-deep", child.TaskID}} {
		in := approveMaterialization(t, s, dir, request.id, request.parent, materializationPayload)
		if _, created, err := s.MaterializeWorkflowTaskRequest(ctx, in, limits); created || !errors.Is(err, domain.ErrTaskMaterializationScope) {
			t.Fatalf("派生树越界申请被接纳: %s created=%v err=%v", request.id, created, err)
		}
	}
	tasks, err := s.ListWorkflowTasks(ctx, in.WorkflowID, "", 100, 0)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("越界申请写入了任务: count=%d err=%v", len(tasks), err)
	}
}

// 删除唯一结果记录校验并以同 ID Task 当作重放，会错误复用另一 Workflow 的手工任务。
func TestM3TaskMaterializationIDCollisionNeverReusesAnotherWorkflow(t *testing.T) {
	ctx := context.Background()
	s, dir := materializationStoreFixture(t)
	in := approveMaterialization(t, s, dir, "collision", "materialization-parent", materializationPayload)
	other := workflowStoreTask("request-collision", "other-workflow", 1, workflowStoreTime())
	createWorkflowForStoreTest(t, s, other.WorkflowID, []domain.WorkflowTask{other}, nil, "other-workflow", strings.Repeat("d", 64), workflowStoreTime())
	if _, created, err := s.MaterializeWorkflowTaskRequest(ctx, in, materializationLimits); created || !errors.Is(err, domain.ErrWorkflowTaskRequestConflict) {
		t.Fatalf("跨 Workflow ID 冲突被当成成功: created=%v err=%v", created, err)
	}
	if _, found, err := s.GetWorkflowTaskMaterialization(ctx, in.WorkflowID, in.RequestID); found || err != nil {
		t.Fatalf("冲突仍生成了物化结果: found=%v err=%v", found, err)
	}
	w, _, err := s.GetWorkflow(ctx, in.WorkflowID)
	if err != nil || w.Revision != in.ExpectedWorkflowRevision {
		t.Fatalf("冲突改变了图 revision: %+v err=%v", w, err)
	}
	if task, found, err := s.GetWorkflowTask(ctx, other.WorkflowID, other.ID); err != nil || !found || task.Title != other.Title {
		t.Fatalf("另一 Workflow 的任务被修改: %+v found=%v err=%v", task, found, err)
	}
}
