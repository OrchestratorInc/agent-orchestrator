package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var (
	ErrTaskMaterializationUnsupported = errors.New("task request kind cannot create a workflow task")
	ErrTaskMaterializationScope       = errors.New("materialized task exceeds the requesting task scope")
)

// TaskRequestMaterializationInput 只引用已批准原件，不允许调用者另带待写入的任务图。
type TaskRequestMaterializationInput struct {
	WorkflowID                                        WorkflowID
	RequestID, RequestSHA                             string
	ExpectedRequestRevision, ExpectedWorkflowRevision int64
	IdempotencyKey, RequestFingerprint                string
	ActorRef, Reason                                  string
	Now                                               time.Time
}

// TaskRequestMaterialization 是一次性物化结果；同一申请重放返回原图版本。
type TaskRequestMaterialization struct {
	RequestID        string
	WorkflowID       WorkflowID
	TaskID           WorkflowTaskID
	WorkflowRevision int64
	CreatedAt        time.Time
}

// TaskMaterializationPatch 是严格解析与完整图验证后的增量，不包含权限或预算修改。
type TaskMaterializationPatch struct {
	Task         WorkflowTask
	Edges        []WorkflowEdge
	Repositories []WorkflowTaskRepository
}

// createTaskPayload 只支持新任务及其前驱；预算、并发、外部仓库和既有任务变更没有输入通道。
type createTaskPayload struct {
	Task struct {
		Kind         WorkflowTaskKind `json:"kind"`
		Title        string           `json:"title"`
		Brief        string           `json:"brief,omitempty"`
		Priority     *int64           `json:"priority,omitempty"`
		AgentHarness AgentHarness     `json:"agentHarness,omitempty"`
		MaxAttempts  *int64           `json:"maxAttempts,omitempty"`
	} `json:"task"`
	DependsOn    []WorkflowTaskID `json:"dependsOn,omitempty"`
	Repositories []struct {
		RepoKey      WorkflowRepoKey `json:"repoKey"`
		AllowedPaths []string        `json:"allowedPaths,omitempty"`
	} `json:"repositories"`
}

// ValidateMaterializationRequest 供首次物化与幂等重放共同核验批准原件，重放也不能接受篡改。
func ValidateMaterializationRequest(request WorkflowTaskRequest, now time.Time) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.State != WorkflowTaskRequestStateApproved || request.DecidedAt.After(now) {
		return ErrInvalidWorkflowTaskRequestDecision
	}
	if request.RequestKind != WorkflowTaskRequestKindCreateTask {
		return ErrTaskMaterializationUnsupported
	}
	digest := sha256.Sum256([]byte(request.RequestJSON))
	if hex.EncodeToString(digest[:]) != request.RequestSHA {
		return ErrWorkflowTaskRequestTampered
	}
	return nil
}

// BuildTaskMaterialization 在存储事务提供的最新快照上重验原件、权限范围和完整 DAG。
// 调用方仍须在同一事务核验新 Operator 授权上下文、revision、预算权限并保存唯一结果。
func BuildTaskMaterialization(request WorkflowTaskRequest, workflow Workflow, requester WorkflowTask,
	graph WorkflowGraph, limits WorkflowGraphLimits, now time.Time,
) (TaskMaterializationPatch, error) {
	if err := ValidateMaterializationRequest(request, now); err != nil {
		return TaskMaterializationPatch{}, err
	}
	if request.WorkflowID != workflow.ID || requester.WorkflowID != workflow.ID || graph.WorkflowID != workflow.ID ||
		request.RequesterTaskID != requester.ID {
		return TaskMaterializationPatch{}, ErrTaskMaterializationScope
	}
	if request.WorkflowRevision != workflow.Revision {
		return TaskMaterializationPatch{}, ErrWorkflowTaskRequestGraphMoved
	}
	if workflow.State != WorkflowStateActive && workflow.State != WorkflowStatePaused {
		return TaskMaterializationPatch{}, ErrInvalidWorkflowState
	}
	var payload createTaskPayload
	decoder := json.NewDecoder(strings.NewReader(request.RequestJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return TaskMaterializationPatch{}, ErrInvalidWorkflowTaskRequestPayload
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return TaskMaterializationPatch{}, ErrInvalidWorkflowTaskRequestPayload
	}
	if !requester.AgentHarness.IsKnown() && requester.AgentHarness != HarnessFake {
		return TaskMaterializationPatch{}, ErrTaskMaterializationScope
	}
	priority, maxAttempts, harness := requester.Priority, int64(1), requester.AgentHarness
	if payload.Task.Priority != nil {
		priority = *payload.Task.Priority
	}
	if payload.Task.MaxAttempts != nil {
		maxAttempts = *payload.Task.MaxAttempts
	}
	if payload.Task.AgentHarness != "" {
		harness = payload.Task.AgentHarness
	}
	if priority > requester.Priority || maxAttempts < 1 || maxAttempts > requester.MaxAttempts || harness != requester.AgentHarness {
		return TaskMaterializationPatch{}, ErrTaskMaterializationScope
	}
	patch := TaskMaterializationPatch{Task: WorkflowTask{
		ID: WorkflowTaskID("request-" + request.ID), WorkflowID: workflow.ID,
		Kind: payload.Task.Kind, Title: strings.TrimSpace(payload.Task.Title), Brief: payload.Task.Brief,
		State: WorkflowTaskStatePending, Priority: priority, AgentHarness: harness,
		MaxAttempts: maxAttempts, Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}}
	if err := patch.Task.Validate(); err != nil {
		return TaskMaterializationPatch{}, err
	}
	if len(payload.Repositories) == 0 {
		return TaskMaterializationPatch{}, ErrTaskMaterializationScope
	}
	scopes := make(map[WorkflowRepoKey]WorkflowTaskRepository)
	for _, binding := range graph.TaskRepositories {
		if binding.TaskID == requester.ID {
			scopes[binding.RepoKey] = binding
		}
	}
	for _, repository := range payload.Repositories {
		parent, ok := scopes[repository.RepoKey]
		if !ok {
			return TaskMaterializationPatch{}, ErrTaskMaterializationScope
		}
		paths, err := narrowTaskPaths(repository.AllowedPaths, parent.AllowedPaths)
		if err != nil {
			return TaskMaterializationPatch{}, err
		}
		patch.Repositories = append(patch.Repositories, WorkflowTaskRepository{
			WorkflowID: workflow.ID, TaskID: patch.Task.ID, RepoKey: repository.RepoKey,
			AllowedPaths: paths, BaseSHA: parent.BaseSHA,
		})
	}
	for _, predecessor := range payload.DependsOn {
		patch.Edges = append(patch.Edges, WorkflowEdge{WorkflowID: workflow.ID, FromTaskID: predecessor, ToTaskID: patch.Task.ID})
	}
	graph.TaskIDs = append(append([]WorkflowTaskID(nil), graph.TaskIDs...), patch.Task.ID)
	graph.Edges = append(append([]WorkflowEdge(nil), graph.Edges...), patch.Edges...)
	graph.TaskRepositories = append(append([]WorkflowTaskRepository(nil), graph.TaskRepositories...), patch.Repositories...)
	if _, err := ValidateWorkflowGraph(graph, limits); err != nil {
		return TaskMaterializationPatch{}, err
	}
	return patch, nil
}

// narrowTaskPaths 按路径分段收窄范围，避免 src-private 被误认为 src 的子目录。
func narrowTaskPaths(requested, allowed []string) ([]string, error) {
	if len(requested) == 0 && len(allowed) != 0 {
		return nil, ErrTaskMaterializationScope
	}
	paths := make([]string, 0, len(requested))
	wholeRepository := false
	for _, raw := range requested {
		path, ok := CanonicalWorkflowPath(raw, false)
		if !ok {
			return nil, ErrTaskMaterializationScope
		}
		permitted := len(allowed) == 0
		for _, scope := range allowed {
			parent, valid := CanonicalWorkflowPath(scope, true)
			if valid && (parent == "" || path == parent || strings.HasPrefix(path, parent+"/")) {
				permitted = true
				break
			}
		}
		if !permitted {
			return nil, ErrTaskMaterializationScope
		}
		if path == "" {
			wholeRepository = true
			continue
		}
		paths = append(paths, path)
	}
	if wholeRepository {
		return []string{}, nil
	}
	return paths, nil
}
