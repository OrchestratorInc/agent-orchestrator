package domain

import "fmt"

// ValidateWorkflowPlanPatch 验证追加图、选定仓库快照及停用闭包，不允许隐藏修改旧任务。
func ValidateWorkflowPlanPatch(graph WorkflowGraph, tasks []WorkflowTask, patch WorkflowPlanPatch, stopped WorkflowTaskID, limits WorkflowGraphLimits) error {
	old := make(map[WorkflowTaskID]WorkflowTask, len(tasks))
	for _, task := range tasks {
		old[task.ID] = task
	}
	retired := make(map[WorkflowTaskID]bool, len(patch.RetireTaskIDs))
	for _, id := range patch.RetireTaskIDs {
		task, found := old[id]
		if !found || retired[id] || task.Planner || task.State == WorkflowTaskStateSucceeded || task.State == WorkflowTaskStateCancelled {
			return fmt.Errorf("cannot retire task %s: %w", id, ErrWorkflowRevisionConflict)
		}
		switch task.State {
		case WorkflowTaskStatePending, WorkflowTaskStateReady, WorkflowTaskStateBlocked, WorkflowTaskStateRetryWait:
		default:
			if id != stopped {
				return ErrWorkflowRevisionConflict
			}
		}
		retired[id] = true
	}
	if stopped != "" && !retired[stopped] {
		return fmt.Errorf("stopped task must be explicitly retired: %w", ErrWorkflowRevisionConflict)
	}
	for _, edge := range graph.Edges {
		if retired[edge.FromTaskID] && !retired[edge.ToTaskID] && old[edge.ToTaskID].State != WorkflowTaskStateSucceeded {
			return fmt.Errorf("retire dependent task %s explicitly: %w", edge.ToTaskID, ErrWorkflowRevisionConflict)
		}
	}
	newIDs := make(map[WorkflowTaskID]bool, len(patch.Tasks))
	for _, task := range patch.Tasks {
		if _, found := old[task.ID]; found || newIDs[task.ID] || task.Planner || task.WorkflowID != graph.WorkflowID {
			return ErrWorkflowRevisionConflict
		}
		if err := task.Validate(); err != nil {
			return err
		}
		newIDs[task.ID] = true
		graph.TaskIDs = append(graph.TaskIDs, task.ID)
	}
	for _, edge := range patch.Dependencies {
		if !newIDs[edge.ToTaskID] || retired[edge.FromTaskID] {
			return ErrWorkflowRevisionConflict
		}
		graph.Edges = append(graph.Edges, edge)
	}
	repos := make(map[WorkflowRepoKey]WorkflowRepository, len(graph.Repositories))
	for _, repo := range graph.Repositories {
		repos[repo.RepoKey] = repo
	}
	scoped := make(map[WorkflowTaskID]bool)
	for _, repo := range patch.TaskRepositories {
		selected, ok := repos[repo.RepoKey]
		if !ok || !newIDs[repo.TaskID] || repo.BaseSHA == "" || repo.BaseSHA != selected.BaseSHA {
			return ErrInvalidWorkflowTaskRepository
		}
		scoped[repo.TaskID] = true
		graph.TaskRepositories = append(graph.TaskRepositories, repo)
	}
	for id := range newIDs {
		if !scoped[id] {
			return ErrInvalidWorkflowTaskRepository
		}
	}
	_, err := ValidateWorkflowGraph(graph, limits)
	return err
}
