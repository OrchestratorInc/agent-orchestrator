package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/providerprofile"
)

// PlanStore 复用已由 Chat Driver 持久化的输出，候选服务不另开 Provider loop。
type PlanStore interface {
	GetWorkflowAttempt(context.Context, domain.WorkflowAttemptID) (domain.WorkflowAttempt, bool, error)
	GetWorkflowAttemptBinding(context.Context, domain.WorkflowAttemptID) (domain.WorkflowAttemptBinding, bool, error)
	GetWorkflowIntervention(context.Context, string) (domain.WorkflowIntervention, bool, error)
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	TurnByID(context.Context, string) (domain.ConversationTurn, error)
	LatestConversationTurn(context.Context, string) (domain.ConversationTurn, error)
	WorkflowReplanStop(context.Context, domain.WorkflowTaskID) (string, error)
	ConversationMessages(context.Context, string) ([]domain.ConversationMessage, error)
	SaveWorkflowPlan(context.Context, domain.WorkflowPlanCandidate) (domain.WorkflowPlanCandidate, error)
	GetWorkflowPlan(context.Context, domain.WorkflowID, string) (domain.WorkflowPlanCandidate, error)
	FindWorkflowPlan(context.Context, string) (domain.WorkflowPlanCandidate, bool, error)
	ListWorkflowPlans(context.Context, domain.WorkflowID) ([]domain.WorkflowPlanCandidate, error)
	ConfirmWorkflowPlan(context.Context, domain.WorkflowPlanCandidate, domain.WorkflowGraphLimits, time.Time) (domain.WorkflowPlanCandidate, error)
}

// PlanInput 指定原始执行和 CAS；手工编辑仅替换受同样校验的候选正文。
type PlanInput struct {
	WorkflowID         domain.WorkflowID        `json:"-"`
	SourceAttemptID    domain.WorkflowAttemptID `json:"sourceAttemptId"`
	SourceTurnID       string                   `json:"sourceTurnId"`
	StopInterventionID string                   `json:"stopInterventionId,omitempty"`
	ExpectedRevision   int64                    `json:"expectedRevision"`
	IdempotencyKey     string                   `json:"idempotencyKey"`
	Edited             *PlanDefinition          `json:"edited,omitempty"`
}

// PlanDefinition 不接受运行状态、权限、物理路径或命令；仓库引用必须属于现有选择。
type PlanDefinition struct {
	Tasks            []TaskInput             `json:"tasks"`
	Dependencies     []DependencyInput       `json:"dependencies"`
	TaskRepositories []TaskRepositoryInput   `json:"taskRepositories"`
	RetireTaskIDs    []domain.WorkflowTaskID `json:"retireTaskIds"`
}

// StartPlan 把自然目标建立为唯一 analysis Task，之后仍由统一调度器准入和启动。
func (s *Service) StartPlan(ctx context.Context, key string, input CreateInput, agent TaskInput) (domain.Workflow, bool, error) {
	if agent.AgentHarness != domain.HarnessCodex {
		return domain.Workflow{}, false, apierr.Invalid("PLANNER_READONLY_UNSUPPORTED", "当前仅 Codex Adapter 支持受控只读规划", nil)
	}
	if len(input.Tasks) > 0 || len(input.Repositories) == 0 {
		return domain.Workflow{}, false, apierr.Invalid("INVALID_PLAN_SCOPE", "规划只接受目标和已选仓库", nil)
	}
	refs, _ := json.Marshal(input.Repositories)
	for _, repo := range input.Repositories {
		if repo.BaseSHA == "" || repo.TargetRef == "" {
			return domain.Workflow{}, false, apierr.Invalid("PLAN_REPO_PIN_REQUIRED", "规划仓库必须明确基线 SHA 和目标分支", nil)
		}
	}
	agent.ID = domain.WorkflowTaskID("plan-" + key)
	agent.Kind = domain.WorkflowTaskKindAnalysis
	agent.Planner = true
	agent.Title = "规划：" + input.Title
	agent.Brief = "仅生成候选 JSON，不修改文件，不执行 Git 或终端命令。返回 {\"tasks\":[{\"id\":\"唯一ID\",\"kind\":\"development\",\"title\":\"任务\",\"brief\":\"说明\",\"agentHarness\":\"" + string(agent.AgentHarness) + "\"}],\"dependencies\":[{\"fromTaskId\":\"前置\",\"toTaskId\":\"后置\"}],\"taskRepositories\":[{\"taskId\":\"任务ID\",\"repoKey\":\"已选key\",\"allowedPaths\":[\"相对目录\"]}],\"retireTaskIds\":[]}。目标：" + input.Goal + "\n已选仓库：" + string(refs)
	input.Tasks = []TaskInput{agent}
	input.Tasks[0].Brief += "\n每个 taskRepositories 项必须包含 baseSha，逐字使用已选仓库的 baseSha；任务 model/effort 必须明确使用所选设置：" + agent.Model + "/" + agent.Effort
	input.Tasks[0].Brief += "\n任务 profile 必须保留所选供应商引用（空值表示原生配置）：" + agent.Profile
	input.TaskRepositories = nil
	for _, repo := range input.Repositories {
		input.TaskRepositories = append(input.TaskRepositories, TaskRepositoryInput{TaskID: agent.ID, RepoKey: repo.RepoKey, BaseSHA: repo.BaseSHA})
	}
	return s.Create(ctx, key, input)
}

// PreparePlan 读取已完成且归属准确的 Chat 输出；UNKNOWN 停止结果不会进入候选链。
func (s *Service) PreparePlan(ctx context.Context, in PlanInput) (domain.WorkflowPlanCandidate, error) {
	ps, ok := s.store.(PlanStore)
	if !ok {
		return domain.WorkflowPlanCandidate{}, apierr.Internal("PLAN_UNAVAILABLE", "规划持久化未装配")
	}
	if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	fp, _ := requestFingerprint(in)
	if prior, found, err := ps.FindWorkflowPlan(ctx, in.IdempotencyKey); err != nil {
		return prior, err
	} else if found {
		if prior.WorkflowID != in.WorkflowID || prior.RequestFingerprint != fp {
			return prior, apierr.Conflict("PLAN_REPLAY_CONFLICT", "请求正文已改变", nil)
		}
		return prior, nil
	}
	definition, err := s.GetDefinition(ctx, in.WorkflowID)
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	if definition.Workflow.Revision != in.ExpectedRevision {
		return domain.WorkflowPlanCandidate{}, apierr.Conflict("PLAN_STALE", "工作流版本已变化", nil)
	}
	a, found, err := ps.GetWorkflowAttempt(ctx, in.SourceAttemptID)
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	if !found || a.WorkflowID != in.WorkflowID || a.SessionID == nil {
		return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_SOURCE_INVALID", "规划 Attempt 没有对应 Session", nil)
	}
	planner := false
	var sourceTask domain.WorkflowTask
	for _, task := range definition.Tasks {
		if task.ID == a.TaskID {
			planner = task.Planner
			sourceTask = task
		}
	}
	if !planner {
		return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_SOURCE_INVALID", "候选必须来自受控只读规划会话", nil)
	}
	stopID, err := ps.WorkflowReplanStop(ctx, a.TaskID)
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	if in.StopInterventionID != "" && in.StopInterventionID != stopID {
		return domain.WorkflowPlanCandidate{}, apierr.Conflict("PLAN_STOP_MISMATCH", "停止回执与本次 Planner 不匹配", nil)
	}
	in.StopInterventionID = stopID
	b, found, err := ps.GetWorkflowAttemptBinding(ctx, a.ID)
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	if !found || b.SessionID == nil || *b.SessionID != *a.SessionID || a.State == domain.WorkflowAttemptStateUnknown {
		return domain.WorkflowPlanCandidate{}, apierr.Conflict("PLAN_SOURCE_UNKNOWN", "规划执行归属不明确", nil)
	}
	var stopped domain.WorkflowTaskID
	if in.StopInterventionID != "" {
		stop, found, err := ps.GetWorkflowIntervention(ctx, in.StopInterventionID)
		if err != nil {
			return domain.WorkflowPlanCandidate{}, err
		}
		if !found || stop.WorkflowID != in.WorkflowID || stop.State != domain.WorkflowInterventionStateStopConfirmed {
			return domain.WorkflowPlanCandidate{}, apierr.Conflict("PLAN_STOP_UNCONFIRMED", "停止尚未可信确认", nil)
		}
		stopped = stop.TaskID
	}
	conversation, err := ps.ConversationForSession(ctx, *a.SessionID)
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	var turn domain.ConversationTurn
	if in.SourceTurnID == "" {
		turn, err = ps.LatestConversationTurn(ctx, conversation.ID)
	} else {
		turn, err = ps.TurnByID(ctx, in.SourceTurnID)
	}
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	if turn.ConversationID != conversation.ID || turn.HandledBySessionID != *a.SessionID || turn.State != domain.TurnStateCompleted {
		return domain.WorkflowPlanCandidate{}, apierr.Conflict("PLAN_TURN_INCOMPLETE", "规划输出尚未完成或属于其他会话", nil)
	}
	var candidate PlanDefinition
	if in.Edited != nil {
		candidate = *in.Edited
	} else {
		messages, err := ps.ConversationMessages(ctx, conversation.ID)
		if err != nil {
			return domain.WorkflowPlanCandidate{}, err
		}
		var output strings.Builder
		for _, msg := range messages {
			if msg.TurnID == turn.ID && msg.Role == domain.MessageRoleAssistant && !msg.Streaming {
				output.WriteString(msg.Text)
			}
		}
		raw := strings.TrimSpace(output.String())
		if len(raw) > 256*1024 {
			return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_OUTPUT_LIMIT", "候选超过大小限制", nil)
		}
		if strings.HasPrefix(raw, "```json\n") && strings.HasSuffix(raw, "```") {
			raw = strings.TrimSuffix(strings.TrimPrefix(raw, "```json\n"), "```")
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&candidate); err != nil {
			return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_OUTPUT_INVALID", err.Error(), nil)
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_OUTPUT_INVALID", "候选包含额外内容", nil)
		}
		// 模型生成的候选不能自行换凭据；只有显式编辑候选可以清除或重新选择引用。
		for i := range candidate.Tasks {
			task := &candidate.Tasks[i]
			if task.Profile != "" && task.Profile != sourceTask.Profile {
				return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_PROFILE_MISMATCH", "规划输出改变了所选供应商", nil)
			}
			task.Profile = sourceTask.Profile
			if sourceTask.Profile != "" {
				if task.Model == "" {
					task.Model = sourceTask.Model
				}
				if task.Effort == "" {
					task.Effort = sourceTask.Effort
				}
			}
		}
	}
	patch, err := s.planPatch(ctx, definition, candidate)
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	graph := domain.WorkflowGraph{WorkflowID: in.WorkflowID, Edges: definition.Edges, Repositories: definition.Repositories, TaskRepositories: definition.TaskRepositories}
	for _, task := range definition.Tasks {
		graph.TaskIDs = append(graph.TaskIDs, task.ID)
	}
	if err := domain.ValidateWorkflowPlanPatch(graph, definition.Tasks, patch, stopped, s.graphLimits); err != nil {
		return domain.WorkflowPlanCandidate{}, apierr.Invalid("PLAN_PATCH_INVALID", err.Error(), nil)
	}
	digest, _ := requestFingerprint(patch)
	result, err := ps.SaveWorkflowPlan(ctx, domain.WorkflowPlanCandidate{ID: "candidate-" + s.newID(), WorkflowID: in.WorkflowID, SourceAttemptID: a.ID, SourceTurnID: turn.ID, StopInterventionID: in.StopInterventionID, ExpectedRevision: in.ExpectedRevision, Digest: digest, Patch: patch, IdempotencyKey: in.IdempotencyKey, RequestFingerprint: fp, CreatedAt: s.clock().UTC()})
	if err != nil {
		return result, mapStoreError("PLAN_PREPARE_FAILED", "候选未保存", err)
	}
	return result, nil
}

// planPatch 仅追加新 ID；现有图与 repository 共同参与正式 DAG 校验。
func (s *Service) planPatch(ctx context.Context, old Definition, in PlanDefinition) (domain.WorkflowPlanPatch, error) {
	patch := domain.WorkflowPlanPatch{RetireTaskIDs: in.RetireTaskIDs}
	if len(in.Tasks) == 0 {
		return patch, apierr.Invalid("PLAN_EMPTY", "候选必须包含任务", nil)
	}
	graph := domain.WorkflowGraph{WorkflowID: old.Workflow.ID, Edges: append([]domain.WorkflowEdge{}, old.Edges...), Repositories: old.Repositories, TaskRepositories: append([]domain.WorkflowTaskRepository{}, old.TaskRepositories...)}
	for _, task := range old.Tasks {
		graph.TaskIDs = append(graph.TaskIDs, task.ID)
	}
	for _, task := range in.Tasks {
		if err := providerprofile.Validate(ctx, s.profiles, task.Profile, task.AgentHarness, task.Model, task.Effort); err != nil {
			return patch, err
		}
		if len(task.Model) > 256 || len(task.Effort) > 32 || (task.Effort != "" && task.Model == "") || strings.ContainsAny(task.Model+task.Effort, "\r\n\x00") || !task.AgentHarness.IsKnown() {
			return patch, apierr.Invalid("PLAN_AGENT_INVALID", "模型/profile/Harness 不受支持", map[string]any{"taskId": task.ID})
		}
		if task.MaxAttempts == 0 {
			task.MaxAttempts = 1
		}
		r := domain.WorkflowTask{ID: task.ID, WorkflowID: old.Workflow.ID, Kind: task.Kind, Title: task.Title, Brief: task.Brief, State: domain.WorkflowTaskStatePending, Priority: task.Priority, AgentHarness: task.AgentHarness, Model: task.Model, Effort: task.Effort, Profile: task.Profile, MaxAttempts: task.MaxAttempts, Revision: 1, CreatedAt: s.clock().UTC(), UpdatedAt: s.clock().UTC()}
		if err := r.Validate(); err != nil {
			return patch, apierr.Invalid("PLAN_TASK_INVALID", err.Error(), nil)
		}
		patch.Tasks = append(patch.Tasks, r)
		graph.TaskIDs = append(graph.TaskIDs, r.ID)
	}
	newIDs := map[domain.WorkflowTaskID]bool{}
	for _, task := range patch.Tasks {
		newIDs[task.ID] = true
	}
	for _, edge := range in.Dependencies {
		if !newIDs[edge.ToTaskID] {
			return patch, apierr.Invalid("PLAN_EXISTING_EDGE", "不能修改旧任务的依赖", nil)
		}
		e := domain.WorkflowEdge{WorkflowID: old.Workflow.ID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID}
		patch.Dependencies = append(patch.Dependencies, e)
		graph.Edges = append(graph.Edges, e)
	}
	for _, repo := range in.TaskRepositories {
		if !newIDs[repo.TaskID] {
			return patch, apierr.Invalid("PLAN_EXISTING_SCOPE", "不能修改旧任务仓库范围", nil)
		}
		r := domain.WorkflowTaskRepository{WorkflowID: old.Workflow.ID, TaskID: repo.TaskID, RepoKey: repo.RepoKey, AllowedPaths: repo.AllowedPaths, BaseSHA: repo.BaseSHA}
		patch.TaskRepositories = append(patch.TaskRepositories, r)
		graph.TaskRepositories = append(graph.TaskRepositories, r)
	}
	if _, err := domain.ValidateWorkflowGraph(graph, s.graphLimits); err != nil {
		return patch, apierr.Invalid("INVALID_WORKFLOW_GRAPH", err.Error(), nil)
	}
	return patch, nil
}

// ListPlans 返回持久候选，重连只读快照，不重新调用 Agent。
func (s *Service) ListPlans(ctx context.Context, w domain.WorkflowID) ([]domain.WorkflowPlanCandidate, error) {
	ps, ok := s.store.(PlanStore)
	if !ok {
		return nil, fmt.Errorf("plan storage unavailable")
	}
	return ps.ListWorkflowPlans(ctx, w)
}

// ConfirmPlan 将摘要和 revision 送入 Store 原子门禁；正式 DAG 仅在确认后改变。
func (s *Service) ConfirmPlan(ctx context.Context, w domain.WorkflowID, id, digest string, revision int64) (domain.WorkflowPlanCandidate, error) {
	ps, ok := s.store.(PlanStore)
	if !ok {
		return domain.WorkflowPlanCandidate{}, fmt.Errorf("plan storage unavailable")
	}
	p, err := ps.GetWorkflowPlan(ctx, w, id)
	if err != nil {
		return p, err
	}
	if p.Digest != digest || p.ExpectedRevision != revision {
		return p, apierr.Conflict("PLAN_STALE", "候选摘要或版本不匹配", nil)
	}
	for _, task := range p.Patch.Tasks {
		if err := providerprofile.Validate(ctx, s.profiles, task.Profile, task.AgentHarness, task.Model, task.Effort); err != nil {
			return p, err
		}
	}
	p, err = ps.ConfirmWorkflowPlan(ctx, p, s.graphLimits, s.clock().UTC())
	if err != nil {
		return p, mapStoreError("PLAN_CONFIRM_FAILED", "候选未应用", err)
	}
	return p, nil
}
