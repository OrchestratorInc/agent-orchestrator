package domain

import (
	"fmt"
	"sort"
	"strings"
)

// WorkflowGraph is the untrusted, ID-level shape checked before persistence.
// TaskIDs are used instead of full WorkflowTask values so a proposed graph can
// be validated before timestamps, revisions, or execution state are assigned.
type WorkflowGraph struct {
	WorkflowID       WorkflowID
	TaskIDs          []WorkflowTaskID
	Edges            []WorkflowEdge
	Repositories     []WorkflowRepository
	TaskRepositories []WorkflowTaskRepository
}

// WorkflowGraphLimits contains mandatory hard bounds for graph validation.
// MaxDepth counts both the root and leaf nodes; MaxFanOut counts outgoing edges.
type WorkflowGraphLimits struct {
	MaxTasks  int
	MaxDepth  int
	MaxFanOut int
}

// WorkflowGraphResult contains a deterministic topological order and the
// longest root-to-task path length for every task.
type WorkflowGraphResult struct {
	TopologicalOrder []WorkflowTaskID
	DepthByTask      map[WorkflowTaskID]int
}

// WorkflowGraphErrorCode is a stable machine-readable DAG validation failure.
type WorkflowGraphErrorCode string

// Workflow graph validation error codes.
const (
	WorkflowGraphErrInvalidLimits           WorkflowGraphErrorCode = "invalid_limits"
	WorkflowGraphErrMissingWorkflowID       WorkflowGraphErrorCode = "missing_workflow_id"
	WorkflowGraphErrInvalidTaskID           WorkflowGraphErrorCode = "invalid_task_id"
	WorkflowGraphErrDuplicateTaskID         WorkflowGraphErrorCode = "duplicate_task_id"
	WorkflowGraphErrTooManyTasks            WorkflowGraphErrorCode = "too_many_tasks"
	WorkflowGraphErrInvalidRepository       WorkflowGraphErrorCode = "invalid_repository"
	WorkflowGraphErrCrossWorkflowRepository WorkflowGraphErrorCode = "cross_workflow_repository"
	WorkflowGraphErrDuplicateRepositoryPath WorkflowGraphErrorCode = "duplicate_repository_path"
	WorkflowGraphErrDuplicateRepoKey        WorkflowGraphErrorCode = "duplicate_repo_key"
	WorkflowGraphErrInvalidTaskRepository   WorkflowGraphErrorCode = "invalid_task_repository"
	WorkflowGraphErrCrossWorkflowTaskRepo   WorkflowGraphErrorCode = "cross_workflow_task_repository"
	WorkflowGraphErrMissingTaskRepoTask     WorkflowGraphErrorCode = "missing_task_repository_task"
	WorkflowGraphErrMissingTaskRepoKey      WorkflowGraphErrorCode = "missing_task_repository_key"
	WorkflowGraphErrDuplicateTaskRepo       WorkflowGraphErrorCode = "duplicate_task_repository"
	WorkflowGraphErrInvalidEdge             WorkflowGraphErrorCode = "invalid_edge"
	WorkflowGraphErrCrossWorkflowEdge       WorkflowGraphErrorCode = "cross_workflow_edge"
	WorkflowGraphErrSelfDependency          WorkflowGraphErrorCode = "self_dependency"
	WorkflowGraphErrMissingDependency       WorkflowGraphErrorCode = "missing_dependency"
	WorkflowGraphErrDuplicateDependency     WorkflowGraphErrorCode = "duplicate_dependency"
	WorkflowGraphErrCycle                   WorkflowGraphErrorCode = "cycle"
	WorkflowGraphErrMaxDepthExceeded        WorkflowGraphErrorCode = "max_depth_exceeded"
	WorkflowGraphErrMaxFanOutExceeded       WorkflowGraphErrorCode = "max_fan_out_exceeded"
)

// WorkflowGraphValidationError identifies the exact task, edge, repository, or
// configured limit that made an untrusted graph invalid.
type WorkflowGraphValidationError struct {
	Code       WorkflowGraphErrorCode
	Message    string
	TaskID     WorkflowTaskID
	FromTaskID WorkflowTaskID
	ToTaskID   WorkflowTaskID
	RepoKey    WorkflowRepoKey
	Cycle      []WorkflowTaskID
	Limit      int
	Actual     int
	Cause      error
}

// Error returns a stable human-readable description of a graph validation failure.
func (e *WorkflowGraphValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return "workflow graph: " + e.Message
	}
	return "workflow graph: " + string(e.Code)
}

// Unwrap preserves the underlying domain validation error when one exists.
func (e *WorkflowGraphValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// FieldPath returns the stable request field path associated with the error code.
func (e *WorkflowGraphValidationError) FieldPath() string {
	if e == nil {
		return ""
	}
	switch e.Code {
	case WorkflowGraphErrInvalidLimits:
		return "limits"
	case WorkflowGraphErrMissingWorkflowID:
		return "workflowId"
	case WorkflowGraphErrInvalidTaskID, WorkflowGraphErrDuplicateTaskID, WorkflowGraphErrTooManyTasks,
		WorkflowGraphErrMaxDepthExceeded:
		return "tasks"
	case WorkflowGraphErrInvalidRepository, WorkflowGraphErrCrossWorkflowRepository,
		WorkflowGraphErrDuplicateRepositoryPath, WorkflowGraphErrDuplicateRepoKey:
		return "repositories"
	case WorkflowGraphErrInvalidTaskRepository, WorkflowGraphErrCrossWorkflowTaskRepo,
		WorkflowGraphErrMissingTaskRepoTask, WorkflowGraphErrMissingTaskRepoKey,
		WorkflowGraphErrDuplicateTaskRepo:
		return "taskRepositories"
	case WorkflowGraphErrInvalidEdge, WorkflowGraphErrCrossWorkflowEdge,
		WorkflowGraphErrSelfDependency, WorkflowGraphErrMissingDependency,
		WorkflowGraphErrDuplicateDependency, WorkflowGraphErrCycle,
		WorkflowGraphErrMaxFanOutExceeded:
		return "dependencies"
	default:
		return ""
	}
}

// ValidateWorkflowGraph validates IDs, dependencies, repository bindings, graph
// bounds, and acyclicity without I/O, LLM calls, or mutation of the input.
func ValidateWorkflowGraph(graph WorkflowGraph, limits WorkflowGraphLimits) (WorkflowGraphResult, error) {
	if limits.MaxTasks < 1 || limits.MaxDepth < 1 || limits.MaxFanOut < 0 {
		return WorkflowGraphResult{}, &WorkflowGraphValidationError{
			Code:    WorkflowGraphErrInvalidLimits,
			Message: "maxTasks and maxDepth must be positive and maxFanOut cannot be negative",
		}
	}
	if strings.TrimSpace(string(graph.WorkflowID)) == "" {
		return WorkflowGraphResult{}, &WorkflowGraphValidationError{
			Code: WorkflowGraphErrMissingWorkflowID, Message: "workflow ID is required",
		}
	}
	if len(graph.TaskIDs) > limits.MaxTasks {
		return WorkflowGraphResult{}, &WorkflowGraphValidationError{
			Code:    WorkflowGraphErrTooManyTasks,
			Message: fmt.Sprintf("task count %d exceeds limit %d", len(graph.TaskIDs), limits.MaxTasks),
			Limit:   limits.MaxTasks, Actual: len(graph.TaskIDs),
		}
	}

	taskIDs := append([]WorkflowTaskID(nil), graph.TaskIDs...)
	sort.Slice(taskIDs, func(i, j int) bool { return taskIDs[i] < taskIDs[j] })
	taskSet := make(map[WorkflowTaskID]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		if strings.TrimSpace(string(taskID)) == "" {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code: WorkflowGraphErrInvalidTaskID, Message: "task ID cannot be blank",
			}
		}
		if _, exists := taskSet[taskID]; exists {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrDuplicateTaskID,
				Message: fmt.Sprintf("duplicate task ID %q", taskID), TaskID: taskID,
			}
		}
		taskSet[taskID] = struct{}{}
	}

	repositoryKeys, err := validateWorkflowGraphRepositories(graph)
	if err != nil {
		return WorkflowGraphResult{}, err
	}
	if err := validateWorkflowGraphTaskRepositories(graph, taskSet, repositoryKeys); err != nil {
		return WorkflowGraphResult{}, err
	}

	adjacency := make(map[WorkflowTaskID][]WorkflowTaskID, len(taskIDs))
	indegree := make(map[WorkflowTaskID]int, len(taskIDs))
	for _, taskID := range taskIDs {
		adjacency[taskID] = nil
		indegree[taskID] = 0
	}

	edges := append([]WorkflowEdge(nil), graph.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromTaskID != edges[j].FromTaskID {
			return edges[i].FromTaskID < edges[j].FromTaskID
		}
		if edges[i].ToTaskID != edges[j].ToTaskID {
			return edges[i].ToTaskID < edges[j].ToTaskID
		}
		return edges[i].WorkflowID < edges[j].WorkflowID
	})
	seenEdges := make(map[workflowGraphEdgeKey]struct{}, len(edges))
	for _, edge := range edges {
		if strings.TrimSpace(string(edge.WorkflowID)) == "" ||
			strings.TrimSpace(string(edge.FromTaskID)) == "" ||
			strings.TrimSpace(string(edge.ToTaskID)) == "" {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code:       WorkflowGraphErrInvalidEdge,
				Message:    "dependency edge requires workflow, source task, and target task IDs",
				FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID,
			}
		}
		if edge.WorkflowID != graph.WorkflowID {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code: WorkflowGraphErrCrossWorkflowEdge,
				Message: fmt.Sprintf("edge %q -> %q belongs to workflow %q, not %q",
					edge.FromTaskID, edge.ToTaskID, edge.WorkflowID, graph.WorkflowID),
				FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID,
			}
		}
		if edge.FromTaskID == edge.ToTaskID {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrSelfDependency,
				Message: fmt.Sprintf("task %q cannot depend on itself", edge.FromTaskID),
				TaskID:  edge.FromTaskID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID,
			}
		}
		if _, exists := taskSet[edge.FromTaskID]; !exists {
			return WorkflowGraphResult{}, missingWorkflowGraphDependency(edge.FromTaskID, edge)
		}
		if _, exists := taskSet[edge.ToTaskID]; !exists {
			return WorkflowGraphResult{}, missingWorkflowGraphDependency(edge.ToTaskID, edge)
		}

		key := workflowGraphEdgeKey{from: edge.FromTaskID, to: edge.ToTaskID}
		if _, exists := seenEdges[key]; exists {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code:       WorkflowGraphErrDuplicateDependency,
				Message:    fmt.Sprintf("duplicate dependency edge %q -> %q", edge.FromTaskID, edge.ToTaskID),
				FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID,
			}
		}
		seenEdges[key] = struct{}{}
		adjacency[edge.FromTaskID] = append(adjacency[edge.FromTaskID], edge.ToTaskID)
		indegree[edge.ToTaskID]++
	}

	for _, taskID := range taskIDs {
		fanOut := len(adjacency[taskID])
		if fanOut > limits.MaxFanOut {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrMaxFanOutExceeded,
				Message: fmt.Sprintf("task %q has fan-out %d, exceeding limit %d", taskID, fanOut, limits.MaxFanOut),
				TaskID:  taskID, Limit: limits.MaxFanOut, Actual: fanOut,
			}
		}
	}

	order, remainingIndegree := stableWorkflowTopologicalOrder(taskIDs, adjacency, indegree)
	if len(order) != len(taskIDs) {
		remaining := make(map[WorkflowTaskID]bool, len(taskIDs)-len(order))
		for _, taskID := range taskIDs {
			remaining[taskID] = remainingIndegree[taskID] > 0
		}
		cycle := findWorkflowGraphCycle(taskIDs, adjacency, remaining)
		return WorkflowGraphResult{}, &WorkflowGraphValidationError{
			Code:    WorkflowGraphErrCycle,
			Message: fmt.Sprintf("dependency cycle detected: %s", formatWorkflowTaskIDs(cycle)),
			Cycle:   cycle,
		}
	}

	depthByTask := make(map[WorkflowTaskID]int, len(taskIDs))
	for _, taskID := range order {
		if depthByTask[taskID] == 0 {
			depthByTask[taskID] = 1
		}
		if depthByTask[taskID] > limits.MaxDepth {
			return WorkflowGraphResult{}, &WorkflowGraphValidationError{
				Code: WorkflowGraphErrMaxDepthExceeded,
				Message: fmt.Sprintf("task %q has depth %d, exceeding limit %d",
					taskID, depthByTask[taskID], limits.MaxDepth),
				TaskID: taskID, Limit: limits.MaxDepth, Actual: depthByTask[taskID],
			}
		}
		for _, next := range adjacency[taskID] {
			candidateDepth := depthByTask[taskID] + 1
			if candidateDepth > depthByTask[next] {
				depthByTask[next] = candidateDepth
			}
		}
	}

	return WorkflowGraphResult{TopologicalOrder: order, DepthByTask: depthByTask}, nil
}

type workflowGraphEdgeKey struct {
	from WorkflowTaskID
	to   WorkflowTaskID
}

func validateWorkflowGraphRepositories(graph WorkflowGraph) (map[WorkflowRepoKey]struct{}, error) {
	repositories := append([]WorkflowRepository(nil), graph.Repositories...)
	sort.Slice(repositories, func(i, j int) bool {
		if repositories[i].RepoKey != repositories[j].RepoKey {
			return repositories[i].RepoKey < repositories[j].RepoKey
		}
		return repositories[i].ProjectID < repositories[j].ProjectID
	})

	repositoryKeys := make(map[WorkflowRepoKey]struct{}, len(repositories))
	repositoryPaths := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		if err := repository.Validate(); err != nil {
			return nil, &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrInvalidRepository,
				Message: fmt.Sprintf("invalid repository binding %q: %v", repository.RepoKey, err),
				RepoKey: repository.RepoKey, Cause: err,
			}
		}
		if repository.WorkflowID != graph.WorkflowID {
			return nil, &WorkflowGraphValidationError{
				Code: WorkflowGraphErrCrossWorkflowRepository,
				Message: fmt.Sprintf("repository %q belongs to workflow %q, not %q",
					repository.RepoKey, repository.WorkflowID, graph.WorkflowID),
				RepoKey: repository.RepoKey,
			}
		}
		if _, exists := repositoryKeys[repository.RepoKey]; exists {
			return nil, &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrDuplicateRepoKey,
				Message: fmt.Sprintf("duplicate repository key %q", repository.RepoKey),
				RepoKey: repository.RepoKey,
			}
		}
		canonicalPath, _ := CanonicalWorkflowPath(repository.PathRef, true)
		pathKey := string(repository.ProjectID) + "\x00" + canonicalPath
		if _, exists := repositoryPaths[pathKey]; exists {
			return nil, &WorkflowGraphValidationError{
				Code: WorkflowGraphErrDuplicateRepositoryPath,
				Message: fmt.Sprintf("project %q has more than one repository binding for path %q",
					repository.ProjectID, canonicalPath),
				RepoKey: repository.RepoKey,
			}
		}
		repositoryKeys[repository.RepoKey] = struct{}{}
		repositoryPaths[pathKey] = struct{}{}
	}
	return repositoryKeys, nil
}

func validateWorkflowGraphTaskRepositories(
	graph WorkflowGraph,
	taskSet map[WorkflowTaskID]struct{},
	repositoryKeys map[WorkflowRepoKey]struct{},
) error {
	bindings := append([]WorkflowTaskRepository(nil), graph.TaskRepositories...)
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].TaskID != bindings[j].TaskID {
			return bindings[i].TaskID < bindings[j].TaskID
		}
		return bindings[i].RepoKey < bindings[j].RepoKey
	})

	seen := make(map[workflowGraphTaskRepoKey]struct{}, len(bindings))
	for _, binding := range bindings {
		if err := binding.Validate(); err != nil {
			return &WorkflowGraphValidationError{
				Code: WorkflowGraphErrInvalidTaskRepository,
				Message: fmt.Sprintf("invalid task repository binding for task %q and repo %q: %v",
					binding.TaskID, binding.RepoKey, err),
				TaskID: binding.TaskID, RepoKey: binding.RepoKey, Cause: err,
			}
		}
		if binding.WorkflowID != graph.WorkflowID {
			return &WorkflowGraphValidationError{
				Code: WorkflowGraphErrCrossWorkflowTaskRepo,
				Message: fmt.Sprintf("task repository binding %q belongs to workflow %q, not %q",
					binding.RepoKey, binding.WorkflowID, graph.WorkflowID),
				TaskID: binding.TaskID, RepoKey: binding.RepoKey,
			}
		}
		if _, exists := taskSet[binding.TaskID]; !exists {
			return &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrMissingTaskRepoTask,
				Message: fmt.Sprintf("repository binding references missing task %q", binding.TaskID),
				TaskID:  binding.TaskID, RepoKey: binding.RepoKey,
			}
		}
		if _, exists := repositoryKeys[binding.RepoKey]; !exists {
			return &WorkflowGraphValidationError{
				Code: WorkflowGraphErrMissingTaskRepoKey,
				Message: fmt.Sprintf("task %q references repository key %q not bound to this workflow",
					binding.TaskID, binding.RepoKey),
				TaskID: binding.TaskID, RepoKey: binding.RepoKey,
			}
		}
		key := workflowGraphTaskRepoKey{taskID: binding.TaskID, repoKey: binding.RepoKey}
		if _, exists := seen[key]; exists {
			return &WorkflowGraphValidationError{
				Code:    WorkflowGraphErrDuplicateTaskRepo,
				Message: fmt.Sprintf("task %q has duplicate repository binding %q", binding.TaskID, binding.RepoKey),
				TaskID:  binding.TaskID, RepoKey: binding.RepoKey,
			}
		}
		seen[key] = struct{}{}
	}
	return nil
}

type workflowGraphTaskRepoKey struct {
	taskID  WorkflowTaskID
	repoKey WorkflowRepoKey
}

func missingWorkflowGraphDependency(taskID WorkflowTaskID, edge WorkflowEdge) *WorkflowGraphValidationError {
	return &WorkflowGraphValidationError{
		Code: WorkflowGraphErrMissingDependency,
		Message: fmt.Sprintf("dependency edge %q -> %q references missing task %q",
			edge.FromTaskID, edge.ToTaskID, taskID),
		TaskID: taskID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID,
	}
}

// stableWorkflowTopologicalOrder uses a min-heap so independent tasks are always
// emitted in lexical ID order, regardless of input order or map iteration.
func stableWorkflowTopologicalOrder(
	taskIDs []WorkflowTaskID,
	adjacency map[WorkflowTaskID][]WorkflowTaskID,
	indegree map[WorkflowTaskID]int,
) ([]WorkflowTaskID, map[WorkflowTaskID]int) {
	remainingIndegree := make(map[WorkflowTaskID]int, len(indegree))
	for taskID, count := range indegree {
		remainingIndegree[taskID] = count
	}

	ready := workflowTaskIDHeap{}
	for _, taskID := range taskIDs {
		if remainingIndegree[taskID] == 0 {
			ready.push(taskID)
		}
	}

	order := make([]WorkflowTaskID, 0, len(taskIDs))
	for len(ready) > 0 {
		taskID := ready.pop()
		order = append(order, taskID)
		for _, next := range adjacency[taskID] {
			remainingIndegree[next]--
			if remainingIndegree[next] == 0 {
				ready.push(next)
			}
		}
	}
	return order, remainingIndegree
}

type workflowTaskIDHeap []WorkflowTaskID

func (h *workflowTaskIDHeap) push(taskID WorkflowTaskID) {
	*h = append(*h, taskID)
	for index := len(*h) - 1; index > 0; {
		parent := (index - 1) / 2
		if (*h)[parent] <= (*h)[index] {
			return
		}
		(*h)[parent], (*h)[index] = (*h)[index], (*h)[parent]
		index = parent
	}
}

func (h *workflowTaskIDHeap) pop() WorkflowTaskID {
	items := *h
	minimum := items[0]
	last := items[len(items)-1]
	*h = items[:len(items)-1]
	if len(*h) == 0 {
		return minimum
	}
	(*h)[0] = last

	for index := 0; ; {
		left := index*2 + 1
		if left >= len(*h) {
			break
		}
		smallest := left
		right := left + 1
		if right < len(*h) && (*h)[right] < (*h)[left] {
			smallest = right
		}
		if (*h)[index] <= (*h)[smallest] {
			break
		}
		(*h)[index], (*h)[smallest] = (*h)[smallest], (*h)[index]
		index = smallest
	}
	return minimum
}

// findWorkflowGraphCycle returns one deterministic closed cycle among nodes left
// by Kahn's algorithm; downstream nodes that are not part of the cycle are skipped.
func findWorkflowGraphCycle(
	taskIDs []WorkflowTaskID,
	adjacency map[WorkflowTaskID][]WorkflowTaskID,
	remaining map[WorkflowTaskID]bool,
) []WorkflowTaskID {
	const (
		visitUnseen uint8 = iota
		visitActive
		visitComplete
	)

	state := make(map[WorkflowTaskID]uint8, len(taskIDs))
	stack := make([]WorkflowTaskID, 0, len(taskIDs))
	stackPosition := make(map[WorkflowTaskID]int, len(taskIDs))
	var visit func(WorkflowTaskID) []WorkflowTaskID
	visit = func(taskID WorkflowTaskID) []WorkflowTaskID {
		state[taskID] = visitActive
		stackPosition[taskID] = len(stack)
		stack = append(stack, taskID)

		for _, next := range adjacency[taskID] {
			if !remaining[next] {
				continue
			}
			switch state[next] {
			case visitUnseen:
				if cycle := visit(next); len(cycle) > 0 {
					return cycle
				}
			case visitActive:
				start := stackPosition[next]
				cycle := append([]WorkflowTaskID(nil), stack[start:]...)
				return append(cycle, next)
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackPosition, taskID)
		state[taskID] = visitComplete
		return nil
	}

	for _, taskID := range taskIDs {
		if remaining[taskID] && state[taskID] == visitUnseen {
			if cycle := visit(taskID); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

func formatWorkflowTaskIDs(taskIDs []WorkflowTaskID) string {
	parts := make([]string, len(taskIDs))
	for i, taskID := range taskIDs {
		parts[i] = string(taskID)
	}
	return strings.Join(parts, " -> ")
}
