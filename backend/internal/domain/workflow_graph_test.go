package domain

import (
	"errors"
	"reflect"
	"testing"
)

func workflowGraphTestLimits() WorkflowGraphLimits {
	return WorkflowGraphLimits{MaxTasks: 10, MaxDepth: 5, MaxFanOut: 3}
}

func workflowGraphWithTasks(taskIDs ...WorkflowTaskID) WorkflowGraph {
	return WorkflowGraph{WorkflowID: "workflow-1", TaskIDs: taskIDs}
}

func requireWorkflowGraphError(t *testing.T, err error, want WorkflowGraphErrorCode) *WorkflowGraphValidationError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected graph error %q, got nil", want)
	}
	var graphErr *WorkflowGraphValidationError
	if !errors.As(err, &graphErr) {
		t.Fatalf("error type = %T, want *WorkflowGraphValidationError: %v", err, err)
	}
	if graphErr.Code != want {
		t.Fatalf("error code = %q, want %q: %v", graphErr.Code, want, err)
	}
	if fieldPath := graphErr.FieldPath(); fieldPath == "" {
		t.Fatalf("error %q has no stable field path", graphErr.Code)
	}
	return graphErr
}

func TestValidateWorkflowGraphReturnsStableTopologicalOrderAndDepth(t *testing.T) {
	graph := workflowGraphWithTasks("f", "d", "b", "e", "a", "c")
	graph.Edges = []WorkflowEdge{
		{WorkflowID: graph.WorkflowID, FromTaskID: "c", ToTaskID: "e"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "a", ToTaskID: "d"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "b", ToTaskID: "d"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "a", ToTaskID: "c"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "d", ToTaskID: "f"},
	}
	graph.Repositories = []WorkflowRepository{{
		WorkflowID: graph.WorkflowID, RepoKey: "backend", ProjectID: "project-1",
	}}
	graph.TaskRepositories = []WorkflowTaskRepository{{
		WorkflowID: graph.WorkflowID, TaskID: "a", RepoKey: "backend",
		AllowedPaths: []string{"backend/internal"},
	}}

	original := graph
	original.TaskIDs = append([]WorkflowTaskID(nil), graph.TaskIDs...)
	original.Edges = append([]WorkflowEdge(nil), graph.Edges...)
	original.Repositories = append([]WorkflowRepository(nil), graph.Repositories...)
	original.TaskRepositories = append([]WorkflowTaskRepository(nil), graph.TaskRepositories...)

	got, err := ValidateWorkflowGraph(graph, workflowGraphTestLimits())
	if err != nil {
		t.Fatalf("ValidateWorkflowGraph() error = %v", err)
	}
	wantOrder := []WorkflowTaskID{"a", "b", "c", "d", "e", "f"}
	if !reflect.DeepEqual(got.TopologicalOrder, wantOrder) {
		t.Fatalf("topological order = %v, want %v", got.TopologicalOrder, wantOrder)
	}
	wantDepth := map[WorkflowTaskID]int{
		"a": 1, "b": 1, "c": 2, "d": 2, "e": 3, "f": 3,
	}
	if !reflect.DeepEqual(got.DepthByTask, wantDepth) {
		t.Fatalf("depth map = %v, want %v", got.DepthByTask, wantDepth)
	}
	if !reflect.DeepEqual(graph, original) {
		t.Fatal("validation mutated the caller's graph")
	}
}

func TestValidateWorkflowGraphAcceptsEmptyDraftGraph(t *testing.T) {
	got, err := ValidateWorkflowGraph(workflowGraphWithTasks(), workflowGraphTestLimits())
	if err != nil {
		t.Fatalf("empty draft graph should be valid: %v", err)
	}
	if len(got.TopologicalOrder) != 0 || len(got.DepthByTask) != 0 {
		t.Fatalf("empty graph result = %+v, want empty order and depths", got)
	}
}

func TestValidateWorkflowGraphRejectsInvalidLimitsAndTaskIDs(t *testing.T) {
	tests := []struct {
		name   string
		graph  WorkflowGraph
		limits WorkflowGraphLimits
		want   WorkflowGraphErrorCode
	}{
		{
			name: "zero task limit", graph: workflowGraphWithTasks("a"),
			limits: WorkflowGraphLimits{MaxDepth: 1, MaxFanOut: 1}, want: WorkflowGraphErrInvalidLimits,
		},
		{
			name: "negative fan out", graph: workflowGraphWithTasks("a"),
			limits: WorkflowGraphLimits{MaxTasks: 1, MaxDepth: 1, MaxFanOut: -1}, want: WorkflowGraphErrInvalidLimits,
		},
		{
			name: "missing workflow id", graph: WorkflowGraph{TaskIDs: []WorkflowTaskID{"a"}},
			limits: workflowGraphTestLimits(), want: WorkflowGraphErrMissingWorkflowID,
		},
		{
			name: "blank task id", graph: workflowGraphWithTasks("a", "  "),
			limits: workflowGraphTestLimits(), want: WorkflowGraphErrInvalidTaskID,
		},
		{
			name: "duplicate task id", graph: workflowGraphWithTasks("a", "b", "a"),
			limits: workflowGraphTestLimits(), want: WorkflowGraphErrDuplicateTaskID,
		},
		{
			name: "task count exceeded", graph: workflowGraphWithTasks("a", "b"),
			limits: WorkflowGraphLimits{MaxTasks: 1, MaxDepth: 2, MaxFanOut: 1}, want: WorkflowGraphErrTooManyTasks,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateWorkflowGraph(tt.graph, tt.limits)
			requireWorkflowGraphError(t, err, tt.want)
		})
	}
}

func TestValidateWorkflowGraphRejectsInvalidRepositoryBindings(t *testing.T) {
	base := workflowGraphWithTasks("task-1")
	validRepo := WorkflowRepository{
		WorkflowID: base.WorkflowID, RepoKey: "backend", ProjectID: "project-1",
	}
	validTaskRepo := WorkflowTaskRepository{
		WorkflowID: base.WorkflowID, TaskID: "task-1", RepoKey: "backend",
		AllowedPaths: []string{"backend/internal"},
	}

	tests := []struct {
		name string
		edit func(*WorkflowGraph)
		want WorkflowGraphErrorCode
	}{
		{
			name: "repository missing project", edit: func(g *WorkflowGraph) {
				repo := validRepo
				repo.ProjectID = ""
				g.Repositories = []WorkflowRepository{repo}
			}, want: WorkflowGraphErrInvalidRepository,
		},
		{
			name: "repository belongs to another workflow", edit: func(g *WorkflowGraph) {
				repo := validRepo
				repo.WorkflowID = "workflow-2"
				g.Repositories = []WorkflowRepository{repo}
			}, want: WorkflowGraphErrCrossWorkflowRepository,
		},
		{
			name: "duplicate repo key", edit: func(g *WorkflowGraph) {
				other := validRepo
				other.ProjectID = "project-2"
				g.Repositories = []WorkflowRepository{validRepo, other}
			}, want: WorkflowGraphErrDuplicateRepoKey,
		},
		{
			name: "duplicate project path under different repo keys", edit: func(g *WorkflowGraph) {
				other := validRepo
				other.RepoKey = "backend-alias"
				other.PathRef = "."
				g.Repositories = []WorkflowRepository{validRepo, other}
			}, want: WorkflowGraphErrDuplicateRepositoryPath,
		},
		{
			name: "invalid task repository path", edit: func(g *WorkflowGraph) {
				g.Repositories = []WorkflowRepository{validRepo}
				binding := validTaskRepo
				binding.AllowedPaths = []string{"src", "src"}
				g.TaskRepositories = []WorkflowTaskRepository{binding}
			}, want: WorkflowGraphErrInvalidTaskRepository,
		},
		{
			name: "task repository belongs to another workflow", edit: func(g *WorkflowGraph) {
				g.Repositories = []WorkflowRepository{validRepo}
				binding := validTaskRepo
				binding.WorkflowID = "workflow-2"
				g.TaskRepositories = []WorkflowTaskRepository{binding}
			}, want: WorkflowGraphErrCrossWorkflowTaskRepo,
		},
		{
			name: "task repository references missing task", edit: func(g *WorkflowGraph) {
				g.Repositories = []WorkflowRepository{validRepo}
				binding := validTaskRepo
				binding.TaskID = "missing"
				g.TaskRepositories = []WorkflowTaskRepository{binding}
			}, want: WorkflowGraphErrMissingTaskRepoTask,
		},
		{
			name: "task repository key is not bound", edit: func(g *WorkflowGraph) {
				binding := validTaskRepo
				g.TaskRepositories = []WorkflowTaskRepository{binding}
			}, want: WorkflowGraphErrMissingTaskRepoKey,
		},
		{
			name: "duplicate task repository binding", edit: func(g *WorkflowGraph) {
				g.Repositories = []WorkflowRepository{validRepo}
				g.TaskRepositories = []WorkflowTaskRepository{validTaskRepo, validTaskRepo}
			}, want: WorkflowGraphErrDuplicateTaskRepo,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := base
			tt.edit(&graph)
			_, err := ValidateWorkflowGraph(graph, workflowGraphTestLimits())
			requireWorkflowGraphError(t, err, tt.want)
		})
	}
}

func TestValidateWorkflowGraphRejectsInvalidDependencies(t *testing.T) {
	base := workflowGraphWithTasks("a", "b")
	tests := []struct {
		name  string
		edges []WorkflowEdge
		want  WorkflowGraphErrorCode
	}{
		{
			name:  "blank edge field",
			edges: []WorkflowEdge{{WorkflowID: base.WorkflowID, FromTaskID: "a"}},
			want:  WorkflowGraphErrInvalidEdge,
		},
		{
			name:  "cross workflow edge",
			edges: []WorkflowEdge{{WorkflowID: "workflow-2", FromTaskID: "a", ToTaskID: "b"}},
			want:  WorkflowGraphErrCrossWorkflowEdge,
		},
		{
			name:  "self dependency",
			edges: []WorkflowEdge{{WorkflowID: base.WorkflowID, FromTaskID: "a", ToTaskID: "a"}},
			want:  WorkflowGraphErrSelfDependency,
		},
		{
			name:  "missing dependency",
			edges: []WorkflowEdge{{WorkflowID: base.WorkflowID, FromTaskID: "a", ToTaskID: "missing"}},
			want:  WorkflowGraphErrMissingDependency,
		},
		{
			name: "duplicate dependency",
			edges: []WorkflowEdge{
				{WorkflowID: base.WorkflowID, FromTaskID: "a", ToTaskID: "b"},
				{WorkflowID: base.WorkflowID, FromTaskID: "a", ToTaskID: "b"},
			},
			want: WorkflowGraphErrDuplicateDependency,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := base
			graph.Edges = tt.edges
			_, err := ValidateWorkflowGraph(graph, workflowGraphTestLimits())
			requireWorkflowGraphError(t, err, tt.want)
		})
	}
}

func TestValidateWorkflowGraphReturnsDeterministicCyclePath(t *testing.T) {
	graph := workflowGraphWithTasks("e", "d", "c", "b", "a")
	graph.Edges = []WorkflowEdge{
		{WorkflowID: graph.WorkflowID, FromTaskID: "c", ToTaskID: "d"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "b", ToTaskID: "c"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "a", ToTaskID: "b"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "c", ToTaskID: "a"},
		{WorkflowID: graph.WorkflowID, FromTaskID: "d", ToTaskID: "e"},
	}
	_, err := ValidateWorkflowGraph(graph, workflowGraphTestLimits())
	graphErr := requireWorkflowGraphError(t, err, WorkflowGraphErrCycle)
	wantCycle := []WorkflowTaskID{"a", "b", "c", "a"}
	if !reflect.DeepEqual(graphErr.Cycle, wantCycle) {
		t.Fatalf("cycle = %v, want %v", graphErr.Cycle, wantCycle)
	}
}

func TestValidateWorkflowGraphEnforcesDepthAndFanOut(t *testing.T) {
	t.Run("max depth", func(t *testing.T) {
		graph := workflowGraphWithTasks("a", "b", "c")
		graph.Edges = []WorkflowEdge{
			{WorkflowID: graph.WorkflowID, FromTaskID: "a", ToTaskID: "b"},
			{WorkflowID: graph.WorkflowID, FromTaskID: "b", ToTaskID: "c"},
		}
		limits := workflowGraphTestLimits()
		limits.MaxDepth = 2
		graphErr := requireWorkflowGraphError(t, graphValidationError(graph, limits), WorkflowGraphErrMaxDepthExceeded)
		if graphErr.TaskID != "c" || graphErr.Actual != 3 {
			t.Fatalf("depth error = %+v, want task c at depth 3", graphErr)
		}
	})

	t.Run("max fan out", func(t *testing.T) {
		graph := workflowGraphWithTasks("a", "b", "c")
		graph.Edges = []WorkflowEdge{
			{WorkflowID: graph.WorkflowID, FromTaskID: "a", ToTaskID: "b"},
			{WorkflowID: graph.WorkflowID, FromTaskID: "a", ToTaskID: "c"},
		}
		limits := workflowGraphTestLimits()
		limits.MaxFanOut = 1
		graphErr := requireWorkflowGraphError(t, graphValidationError(graph, limits), WorkflowGraphErrMaxFanOutExceeded)
		if graphErr.TaskID != "a" || graphErr.Actual != 2 {
			t.Fatalf("fan-out error = %+v, want task a with fan-out 2", graphErr)
		}
	})
}

func graphValidationError(graph WorkflowGraph, limits WorkflowGraphLimits) error {
	_, err := ValidateWorkflowGraph(graph, limits)
	return err
}
