package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// materializationFixture 构造一份已批准原件与现有图，测试只替换待验证的申请内容。
func materializationFixture(payload string) (WorkflowTaskRequest, Workflow, WorkflowTask, WorkflowGraph, time.Time) {
	now := time.Date(2026, time.November, 5, 8, 0, 0, 0, time.UTC)
	human := WorkflowTaskRequestDeciderHuman
	digest := sha256.Sum256([]byte(payload))
	request := WorkflowTaskRequest{ID: "change", WorkflowID: "flow", RequesterTaskID: "parent",
		RequestKind: WorkflowTaskRequestKindCreateTask, RequestJSON: payload, RequestSHA: hex.EncodeToString(digest[:]),
		State: WorkflowTaskRequestStateApproved, DecidedByKind: &human, DecidedByRef: "operator", DecidedAt: &now,
		WorkflowRevision: 2, Revision: 2, CreatedAt: now, UpdatedAt: now}
	workflow := Workflow{ID: "flow", State: WorkflowStateActive, Revision: 2}
	parent := WorkflowTask{ID: "parent", WorkflowID: "flow", Priority: 2, MaxAttempts: 2, AgentHarness: HarnessCodex}
	graph := WorkflowGraph{WorkflowID: "flow", TaskIDs: []WorkflowTaskID{"parent"},
		Repositories:     []WorkflowRepository{{WorkflowID: "flow", RepoKey: "repo", ProjectID: "project"}},
		TaskRepositories: []WorkflowTaskRepository{{WorkflowID: "flow", TaskID: "parent", RepoKey: "repo", AllowedPaths: []string{"src"}, BaseSHA: "pinned"}}}
	return request, workflow, parent, graph, now
}

// 删除完整 DAG 校验会接纳自依赖；删除范围分段比较会接纳名称相近的兄弟目录。
func TestTaskMaterializationNarrowsAnApprovedGraph(t *testing.T) {
	payload := `{"task":{"kind":"testing","title":"检查修改"},"dependsOn":["parent"],"repositories":[{"repoKey":"repo","allowedPaths":["src/tests"]}]}`
	request, workflow, parent, graph, now := materializationFixture(payload)
	limits := WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8}
	patch, err := BuildTaskMaterialization(request, workflow, parent, graph, limits, now)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Task.ID != "request-change" || patch.Task.State != WorkflowTaskStatePending || patch.Task.MaxAttempts != 1 ||
		patch.Task.Priority != parent.Priority || patch.Task.AgentHarness != parent.AgentHarness {
		t.Fatalf("错误的新任务边界: %+v", patch.Task)
	}
	if len(patch.Edges) != 1 || patch.Edges[0].FromTaskID != parent.ID ||
		len(patch.Repositories) != 1 || patch.Repositories[0].BaseSHA != "pinned" || patch.Repositories[0].AllowedPaths[0] != "src/tests" {
		t.Fatalf("依赖或仓库范围不正确: %+v", patch)
	}
	if len(graph.TaskIDs) != 1 || len(graph.TaskRepositories) != 1 {
		t.Fatal("校验修改了调用方的原图")
	}
}

// 删除 strict decoder、审批/SHA/revision 或请求者权限门禁会分别使对应反例错误获准。
func TestTaskMaterializationRejectsUnapprovedOrExpandedWork(t *testing.T) {
	valid := `{"task":{"kind":"testing","title":"检查修改"},"repositories":[{"repoKey":"repo","allowedPaths":["src/tests"]}]}`
	cases := []struct {
		name    string
		payload string
		mutate  func(*WorkflowTaskRequest)
		want    error
	}{
		{name: "超出路径分段", payload: `{"task":{"kind":"testing","title":"x"},"repositories":[{"repoKey":"repo","allowedPaths":["src-private"]}]}`, want: ErrTaskMaterializationScope},
		{name: "隐式扩大为整个仓库", payload: `{"task":{"kind":"testing","title":"x"},"repositories":[{"repoKey":"repo"}]}`, want: ErrTaskMaterializationScope},
		{name: "额外预算字段", payload: `{"task":{"kind":"testing","title":"x"},"budget":999,"repositories":[{"repoKey":"repo","allowedPaths":["src"]}]}`, want: ErrInvalidWorkflowTaskRequestPayload},
		{name: "提升重试次数", payload: `{"task":{"kind":"testing","title":"x","maxAttempts":3},"repositories":[{"repoKey":"repo","allowedPaths":["src"]}]}`, want: ErrTaskMaterializationScope},
		{name: "伪造已批准原件", payload: valid, mutate: func(r *WorkflowTaskRequest) {
			r.RequestSHA = "0000000000000000000000000000000000000000000000000000000000000000"
		}, want: ErrWorkflowTaskRequestTampered},
		{name: "旧图批准", payload: valid, mutate: func(r *WorkflowTaskRequest) { r.WorkflowRevision = 1 }, want: ErrWorkflowTaskRequestGraphMoved},
		{name: "已拒绝", payload: valid, mutate: func(r *WorkflowTaskRequest) { r.State = WorkflowTaskRequestStateRejected }, want: ErrInvalidWorkflowTaskRequestDecision},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, workflow, parent, graph, now := materializationFixture(tc.payload)
			if tc.mutate != nil {
				tc.mutate(&request)
			}
			_, err := BuildTaskMaterialization(request, workflow, parent, graph,
				WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8}, now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("期望 %v，实际 %v", tc.want, err)
			}
		})
	}
	request, workflow, parent, graph, now := materializationFixture(`{"task":{"kind":"testing","title":"x"},"dependsOn":["request-change"],"repositories":[{"repoKey":"repo","allowedPaths":["src"]}]}`)
	_, err := BuildTaskMaterialization(request, workflow, parent, graph, WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8}, now)
	var graphErr *WorkflowGraphValidationError
	if !errors.As(err, &graphErr) || graphErr.Code != WorkflowGraphErrSelfDependency {
		t.Fatalf("自依赖未被完整图检查拒绝: %v", err)
	}
	// 删除父 Harness 的固定身份门禁会允许调度时从可变项目配置重新选择执行者。
	request, workflow, parent, graph, now = materializationFixture(valid)
	parent.AgentHarness = ""
	_, err = BuildTaskMaterialization(request, workflow, parent, graph, WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8}, now)
	if !errors.Is(err, ErrTaskMaterializationScope) {
		t.Fatalf("未固定父适配器仍可物化: %v", err)
	}
}
