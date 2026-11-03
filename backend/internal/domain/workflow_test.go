package domain

import (
	"errors"
	"testing"
	"time"
)

func workflowTestTime() time.Time {
	return time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
}

func TestWorkflowValidate(t *testing.T) {
	now := workflowTestTime()
	valid := Workflow{
		ID: "workflow-1", Title: "Implement API", Goal: "Ship the workflow API",
		State: WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid workflow: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Workflow)
		want error
	}{
		{name: "missing id", edit: func(w *Workflow) { w.ID = "" }, want: ErrInvalidWorkflow},
		{name: "blank title", edit: func(w *Workflow) { w.Title = "  " }, want: ErrInvalidWorkflow},
		{name: "blank goal", edit: func(w *Workflow) { w.Goal = "" }, want: ErrInvalidWorkflow},
		{name: "zero revision", edit: func(w *Workflow) { w.Revision = 0 }, want: ErrInvalidWorkflow},
		{name: "zero created time", edit: func(w *Workflow) { w.CreatedAt = time.Time{} }, want: ErrInvalidWorkflow},
		{name: "updated before created", edit: func(w *Workflow) { w.UpdatedAt = now.Add(-time.Second) }, want: ErrInvalidWorkflow},
		{name: "unknown state", edit: func(w *Workflow) { w.State = "unknown" }, want: ErrInvalidWorkflowState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := valid
			tt.edit(&rec)
			if err := rec.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestWorkflowTaskValidate(t *testing.T) {
	now := workflowTestTime()
	valid := WorkflowTask{
		ID: "task-1", WorkflowID: "workflow-1", Kind: WorkflowTaskKindDevelopment,
		Title: "Implement storage", State: WorkflowTaskStatePending,
		MaxAttempts: 2, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid task: %v", err)
	}

	tests := []struct {
		name string
		edit func(*WorkflowTask)
		want error
	}{
		{name: "missing workflow", edit: func(task *WorkflowTask) { task.WorkflowID = "" }, want: ErrInvalidWorkflowTask},
		{name: "missing title", edit: func(task *WorkflowTask) { task.Title = " " }, want: ErrInvalidWorkflowTask},
		{name: "zero max attempts", edit: func(task *WorkflowTask) { task.MaxAttempts = 0 }, want: ErrInvalidWorkflowTask},
		{name: "unknown kind", edit: func(task *WorkflowTask) { task.Kind = "deploy" }, want: ErrInvalidWorkflowTaskKind},
		{name: "unknown state", edit: func(task *WorkflowTask) { task.State = "queued" }, want: ErrInvalidWorkflowTaskState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := valid
			tt.edit(&rec)
			if err := rec.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestWorkflowEdgeValidateRejectsSelfDependency(t *testing.T) {
	valid := WorkflowEdge{WorkflowID: "workflow-1", FromTaskID: "task-a", ToTaskID: "task-b"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid edge: %v", err)
	}
	valid.ToTaskID = valid.FromTaskID
	if err := valid.Validate(); !errors.Is(err, ErrInvalidWorkflowEdge) {
		t.Fatalf("self edge error = %v, want %v", err, ErrInvalidWorkflowEdge)
	}
}

func TestWorkflowTaskRepositoryValidate(t *testing.T) {
	valid := WorkflowTaskRepository{
		WorkflowID: "workflow-1", TaskID: "task-1", RepoKey: "backend",
		AllowedPaths: []string{"backend/internal", "docs"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid task repository: %v", err)
	}

	tests := []struct {
		name  string
		paths []string
	}{
		{name: "blank path", paths: []string{"backend", "  "}},
		{name: "duplicate path", paths: []string{"backend", "backend"}},
		{name: "canonical duplicate path", paths: []string{"backend//src", "backend/src"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := valid
			rec.AllowedPaths = tt.paths
			if err := rec.Validate(); !errors.Is(err, ErrInvalidWorkflowTaskRepository) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidWorkflowTaskRepository)
			}
		})
	}
}

func TestWorkflowAttemptValidateRequiresLeaseForClaimedStates(t *testing.T) {
	now := workflowTestTime()
	valid := WorkflowAttempt{
		ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
		AttemptNo: 1, WorkerID: "worker-1", LeaseToken: "lease-1",
		LeaseUntil: timePtr(now.Add(time.Minute)), State: WorkflowAttemptStateClaimed,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid claimed attempt: %v", err)
	}

	valid.LeaseToken = ""
	if err := valid.Validate(); !errors.Is(err, ErrInvalidWorkflowAttemptLease) {
		t.Fatalf("missing lease token error = %v, want %v", err, ErrInvalidWorkflowAttemptLease)
	}
	valid.State = WorkflowAttemptStateUnknown
	valid.LeaseUntil = nil
	if err := valid.Validate(); err != nil {
		t.Fatalf("unknown attempt without active lease: %v", err)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestWorkflowRepositoryValidateRejectsUnsafePathRef(t *testing.T) {
	valid := WorkflowRepository{
		WorkflowID: "workflow-1", RepoKey: "backend", ProjectID: "project-1",
	}
	for _, pathRef := range []string{"../outside", "/absolute/path", "C:/outside", "backend\\src"} {
		t.Run(pathRef, func(t *testing.T) {
			repository := valid
			repository.PathRef = pathRef
			if err := repository.Validate(); !errors.Is(err, ErrInvalidWorkflowRepository) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidWorkflowRepository)
			}
		})
	}
}

func TestWorkflowTaskRepositoryValidateRejectsUnsafeAllowedPaths(t *testing.T) {
	base := WorkflowTaskRepository{
		WorkflowID: "workflow-1", TaskID: "task-1", RepoKey: "backend",
	}
	for _, allowedPath := range []string{"../outside", "/absolute/path", "C:/outside", "backend\\src"} {
		t.Run(allowedPath, func(t *testing.T) {
			binding := base
			binding.AllowedPaths = []string{allowedPath}
			if err := binding.Validate(); !errors.Is(err, ErrInvalidWorkflowTaskRepository) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidWorkflowTaskRepository)
			}
		})
	}
}
