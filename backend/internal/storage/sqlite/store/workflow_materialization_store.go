package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// MaterializeWorkflowTaskRequest 原子保存新图、不可变预算父子关系、审计与幂等结果。
// 授权由 Workflow 服务的本次 Operator 门禁提供；ActorRef 不是独立凭据。
func (s *Store) MaterializeWorkflowTaskRequest(ctx context.Context, in domain.TaskRequestMaterializationInput, limits domain.WorkflowGraphLimits) (domain.TaskRequestMaterialization, bool, error) {
	sha, err := hex.DecodeString(in.RequestSHA)
	if err != nil || len(sha) != 32 || len(in.RequestFingerprint) != 64 || strings.TrimSpace(in.IdempotencyKey) == "" ||
		strings.TrimSpace(in.ActorRef) == "" || !domain.ValidSchedulerReason(in.Reason) || in.Now.IsZero() ||
		in.ExpectedRequestRevision < 1 || in.ExpectedWorkflowRevision < 1 || in.ExpectedWorkflowRevision == math.MaxInt64 ||
		limits.MaxTasks < 1 || limits.MaxDepth < 1 || limits.MaxFanOut < 0 {
		return domain.TaskRequestMaterialization{}, false, domain.ErrInvalidWorkflowTaskRequestPayload
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.TaskRequestMaterialization
	created := false
	err = s.inSchedulerTx(ctx, "materialize task request", func(q *gen.Queries) error {
		request, found, err := s.loadTaskRequest(ctx, q, in.WorkflowID, in.RequestID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowTaskRequestNotFound
		}
		if err := domain.ValidateMaterializationRequest(request, in.Now); err != nil {
			return err
		}
		if request.RequestSHA != in.RequestSHA || request.Revision != in.ExpectedRequestRevision || request.WorkflowRevision != in.ExpectedWorkflowRevision {
			return domain.ErrWorkflowTaskRequestConflict
		}
		key, keyErr := q.GetTaskMaterializationKey(ctx, in.IdempotencyKey)
		if keyErr != nil && !errors.Is(keyErr, sql.ErrNoRows) {
			return keyErr
		}
		if keyErr == nil && (key.RequestID != in.RequestID || key.RequestFingerprint != in.RequestFingerprint) {
			return domain.ErrWorkflowIdempotencyConflict
		}
		prior, priorErr := q.GetTaskMaterialization(ctx, in.RequestID)
		if priorErr == nil {
			if prior.WorkflowID != string(in.WorkflowID) || prior.RequestSha != in.RequestSHA || prior.RequestRevision != in.ExpectedRequestRevision || prior.SourceWorkflowRevision != in.ExpectedWorkflowRevision {
				return domain.ErrWorkflowTaskRequestConflict
			}
			if errors.Is(keyErr, sql.ErrNoRows) {
				if err := q.InsertTaskMaterializationKey(ctx, gen.InsertTaskMaterializationKeyParams{IdempotencyKey: in.IdempotencyKey, RequestFingerprint: in.RequestFingerprint, RequestID: in.RequestID}); err != nil {
					return err
				}
			}
			result = taskMaterializationFromGen(prior)
			return nil
		}
		if !errors.Is(priorErr, sql.ErrNoRows) {
			return priorErr
		}
		if keyErr == nil {
			return domain.ErrWorkflowIdempotencyConflict
		}
		w, err := q.GetWorkflow(ctx, in.WorkflowID)
		if err != nil {
			return err
		}
		if w.Revision != in.ExpectedWorkflowRevision {
			return domain.ErrWorkflowTaskRequestGraphMoved
		}
		parent, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: in.WorkflowID, ID: request.RequesterTaskID})
		if err != nil {
			return err
		}
		ancestors, err := q.ListTaskBudgetAncestors(ctx, string(parent.ID))
		if err != nil {
			return err
		}
		children, err := q.CountMaterializedChildren(ctx, string(parent.ID))
		if err != nil {
			return err
		}
		if len(ancestors) >= limits.MaxDepth || children >= int64(limits.MaxFanOut) {
			return domain.ErrTaskMaterializationScope
		}
		graph, err := loadMaterializationGraph(ctx, q, in.WorkflowID, limits.MaxTasks)
		if err != nil {
			return err
		}
		patch, err := domain.BuildTaskMaterialization(request, workflowFromGen(w), workflowTaskFromGen(parent), graph, limits, in.Now.UTC())
		if err != nil {
			return err
		}
		if err := q.InsertWorkflowTask(ctx, workflowTaskToInsert(patch.Task)); err != nil {
			return err
		}
		for _, edge := range patch.Edges {
			if err := q.InsertWorkflowEdge(ctx, gen.InsertWorkflowEdgeParams{WorkflowID: edge.WorkflowID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID}); err != nil {
				return err
			}
		}
		for _, repository := range patch.Repositories {
			insert, err := workflowTaskRepositoryToInsert(repository)
			if err != nil {
				return err
			}
			if err := q.InsertWorkflowTaskRepository(ctx, insert); err != nil {
				return err
			}
		}
		changed, err := q.AdvanceMaterializationWorkflow(ctx, gen.AdvanceMaterializationWorkflowParams{WorkflowID: in.WorkflowID, ExpectedRevision: in.ExpectedWorkflowRevision, Now: in.Now.UTC()})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrWorkflowRevisionConflict
		}
		if err := q.InsertTaskMaterialization(ctx, gen.InsertTaskMaterializationParams{RequestID: request.ID, WorkflowID: string(in.WorkflowID), ParentTaskID: string(parent.ID), ChildTaskID: string(patch.Task.ID), RequestSha: in.RequestSHA, RequestRevision: request.Revision, SourceWorkflowRevision: w.Revision, WorkflowRevision: w.Revision + 1, ActorRef: in.ActorRef, Reason: in.Reason, CreatedAt: in.Now.UTC()}); err != nil {
			return err
		}
		if err := q.InsertTaskMaterializationKey(ctx, gen.InsertTaskMaterializationKeyParams{IdempotencyKey: in.IdempotencyKey, RequestFingerprint: in.RequestFingerprint, RequestID: in.RequestID}); err != nil {
			return err
		}
		if _, err := q.RefreshWorkflowReadyTasks(ctx, gen.RefreshWorkflowReadyTasksParams{WorkflowID: in.WorkflowID, UpdatedAt: in.Now.UTC()}); err != nil {
			return err
		}
		result = domain.TaskRequestMaterialization{RequestID: request.ID, WorkflowID: in.WorkflowID, TaskID: patch.Task.ID, WorkflowRevision: w.Revision + 1, CreatedAt: in.Now.UTC()}
		created = true
		return nil
	})
	var constraint *moderncsqlite.Error
	if errors.As(err, &constraint) && (constraint.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY || constraint.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE) {
		err = domain.ErrWorkflowTaskRequestConflict
	}
	if err != nil {
		return domain.TaskRequestMaterialization{}, false, err
	}
	return result, created, nil
}

// GetWorkflowTaskMaterialization 只返回指定 Workflow 中已经持久化的结果。
func (s *Store) GetWorkflowTaskMaterialization(ctx context.Context, workflowID domain.WorkflowID, requestID string) (domain.TaskRequestMaterialization, bool, error) {
	r, err := s.qr.GetTaskMaterialization(ctx, requestID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && r.WorkflowID != string(workflowID) {
		return domain.TaskRequestMaterialization{}, false, nil
	}
	return taskMaterializationFromGen(r), err == nil, err
}

// taskMaterializationFromGen 不向上层返回原始请求、仓库路径或内部审计身份。
func taskMaterializationFromGen(r gen.WorkflowTaskMaterialization) domain.TaskRequestMaterialization {
	return domain.TaskRequestMaterialization{RequestID: r.RequestID, WorkflowID: domain.WorkflowID(r.WorkflowID), TaskID: domain.WorkflowTaskID(r.ChildTaskID), WorkflowRevision: r.WorkflowRevision, CreatedAt: r.CreatedAt}
}

// loadMaterializationGraph 在同一事务读取完整图；超限数据库也不能通过截断伪装为合法小图。
func loadMaterializationGraph(ctx context.Context, q *gen.Queries, workflowID domain.WorkflowID, maxTasks int) (domain.WorkflowGraph, error) {
	graph := domain.WorkflowGraph{WorkflowID: workflowID}
	tasks, err := q.ListWorkflowTasks(ctx, gen.ListWorkflowTasksParams{WorkflowID: workflowID, PageLimit: int64(maxTasks) + 1})
	if err != nil {
		return graph, err
	}
	if len(tasks) >= maxTasks {
		return graph, domain.ErrTaskMaterializationScope
	}
	for _, task := range tasks {
		graph.TaskIDs = append(graph.TaskIDs, task.ID)
	}
	repositories, err := q.ListWorkflowRepositories(ctx, workflowID)
	if err != nil {
		return graph, err
	}
	for _, repository := range repositories {
		graph.Repositories = append(graph.Repositories, workflowRepositoryFromGen(repository))
	}
	edges, err := q.ListWorkflowEdges(ctx, workflowID)
	if err != nil {
		return graph, err
	}
	for _, edge := range edges {
		graph.Edges = append(graph.Edges, domain.WorkflowEdge{WorkflowID: edge.WorkflowID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID})
	}
	bindings, err := q.ListMaterializationTaskRepositories(ctx, workflowID)
	if err != nil {
		return graph, err
	}
	for _, binding := range bindings {
		var paths []string
		if err := json.Unmarshal([]byte(binding.AllowedPathsJson), &paths); err != nil {
			return graph, err
		}
		if !slices.Contains(graph.TaskIDs, binding.TaskID) {
			return graph, domain.ErrTaskMaterializationScope
		}
		graph.TaskRepositories = append(graph.TaskRepositories, domain.WorkflowTaskRepository{WorkflowID: binding.WorkflowID, TaskID: binding.TaskID, RepoKey: binding.RepoKey, AllowedPaths: paths, BaseSHA: binding.BaseSha})
	}
	return graph, nil
}
