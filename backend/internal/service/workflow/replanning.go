package workflow

import (
	"context"
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// ReplanInput 限定同一 Workflow 的可信停止与新只读 Planner；不接受替换旧图。
type ReplanInput struct {
	WorkflowID         domain.WorkflowID   `json:"-"`
	StopInterventionID string              `json:"stopInterventionId"`
	ExpectedRevision   int64               `json:"expectedRevision"`
	IdempotencyKey     string              `json:"idempotencyKey"`
	AgentHarness       domain.AgentHarness `json:"agentHarness"`
	Model              string              `json:"model"`
	Effort             string              `json:"effort,omitempty"`
	Profile            string              `json:"profile,omitempty"`
}

// StartReplan 持久化收尾意图与新 Planner，调度器先释放旧物理资源再按原预算准入。
func (s *Service) StartReplan(ctx context.Context, in ReplanInput) (domain.WorkflowTask, bool, error) {
	if in.AgentHarness != domain.HarnessCodex {
		return domain.WorkflowTask{}, false, apierr.Invalid("PLANNER_READONLY_UNSUPPORTED", "当前仅 Codex Adapter 支持受控只读规划", nil)
	}
	if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
		return domain.WorkflowTask{}, false, err
	}
	st, ok := s.store.(interface {
		BeginWorkflowReplan(context.Context, domain.WorkflowReplanInput, domain.WorkflowGraphLimits) (domain.WorkflowTask, bool, error)
		FindWorkflowReplan(context.Context, domain.WorkflowID, string, string) (domain.WorkflowTask, bool, error)
	})
	if !ok {
		return domain.WorkflowTask{}, false, apierr.Internal("PLAN_UNAVAILABLE", "重规划未装配")
	}
	fingerprint, err := requestFingerprint(in)
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	if prior, found, err := st.FindWorkflowReplan(ctx, in.WorkflowID, in.IdempotencyKey, fingerprint); err != nil {
		return prior, false, mapStoreError("REPLAN_START_FAILED", "重规划未启动", err)
	} else if found {
		return prior, false, nil
	}
	old, err := s.GetDefinition(ctx, in.WorkflowID)
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	graph, err := json.Marshal(old)
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	agent := TaskInput{ID: domain.WorkflowTaskID("replan-" + in.IdempotencyKey), Kind: domain.WorkflowTaskKindAnalysis, Title: "重规划：" + old.Workflow.Title, AgentHarness: in.AgentHarness, Model: in.Model, Effort: in.Effort, Profile: in.Profile, MaxAttempts: 1,
		Brief: "只读规划。请根据目标和旧图生成替代候选，禁止修改文件或执行命令。仅输出 JSON，字段为 tasks、dependencies、taskRepositories（包含选定 repoKey、baseSha、allowedPaths）、retireTaskIds。只能追加新任务 ID，不得重复已成功任务；明确停用停止任务及无法继续的后继任务。可信停止回执：" + in.StopInterventionID + "\n当前目标与旧图：" + string(graph)}
	patch, err := s.planPatch(old, PlanDefinition{Tasks: []TaskInput{agent}})
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	task := patch.Tasks[0]
	task.Planner = true
	var scopes []domain.WorkflowTaskRepository
	for _, repo := range old.Repositories {
		scopes = append(scopes, domain.WorkflowTaskRepository{WorkflowID: in.WorkflowID, TaskID: task.ID, RepoKey: repo.RepoKey, BaseSHA: repo.BaseSHA})
	}
	result, created, err := st.BeginWorkflowReplan(ctx, domain.WorkflowReplanInput{WorkflowID: in.WorkflowID, StopInterventionID: in.StopInterventionID, ExpectedRevision: in.ExpectedRevision, Planner: task, Repositories: scopes, IdempotencyKey: in.IdempotencyKey, RequestFingerprint: fingerprint}, s.graphLimits)
	if err != nil {
		return result, false, mapStoreError("REPLAN_START_FAILED", "重规划未启动", err)
	}
	return result, created, nil
}
