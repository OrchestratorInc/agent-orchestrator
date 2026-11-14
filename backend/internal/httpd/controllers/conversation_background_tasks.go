package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type backgroundTasksService interface {
	BackgroundTasks(context.Context, domain.SessionID) (chatsvc.BackgroundTaskSnapshot, error)
	ListBackgroundTasks(context.Context, domain.ProjectID) ([]chatsvc.BackgroundTaskSnapshot, error)
	StopBackgroundTask(context.Context, domain.SessionID, string) error
	BackgroundTaskOutput(context.Context, domain.SessionID, string) (ports.ChatBackgroundTaskOutput, error)
}

func (c *ConversationsController) backgroundTasks(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(backgroundTasksService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/background-tasks")
		return
	}
	reading, err := svc.BackgroundTasks(r.Context(), sessionID(r))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, backgroundTaskResponse(reading))
}

func (c *ConversationsController) listBackgroundTasks(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(backgroundTasksService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/background-tasks")
		return
	}
	readings, err := svc.ListBackgroundTasks(r.Context(), domain.ProjectID(r.URL.Query().Get("projectId")))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	out := ListBackgroundTasksResponse{Sessions: make([]BackgroundTasksResponse, 0, len(readings))}
	for _, reading := range readings {
		out.Sessions = append(out.Sessions, backgroundTaskResponse(reading))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ConversationsController) stopBackgroundTask(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(backgroundTasksService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/background-tasks/{taskId}/stop")
		return
	}
	taskID, ok := backgroundTaskID(w, r)
	if !ok {
		return
	}
	err := svc.StopBackgroundTask(r.Context(), sessionID(r), taskID)
	if errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "BACKGROUND_TASK_NOT_STOPPABLE", "this background task cannot be stopped", nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) backgroundTaskOutput(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(backgroundTasksService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/background-tasks/{taskId}/output")
		return
	}
	taskID, ok := backgroundTaskID(w, r)
	if !ok {
		return
	}
	out := BackgroundTaskOutputResponse{SessionID: sessionID(r), TaskID: taskID, Availability: "ready"}
	output, err := svc.BackgroundTaskOutput(r.Context(), out.SessionID, taskID)
	switch {
	case errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable):
		// A harness that keeps no output is an answer, not a failure.
		out.Availability = "unavailable"
	case err != nil:
		writeConversationError(w, r, err)
		return
	default:
		out.Output, out.Truncated = output.Text, output.Truncated
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// backgroundTaskID reads the task path segment, writing a 400 when it is
// malformed. Chi routes on RawPath when present. Otherwise Path is already
// decoded by net/http; unescaping it again would corrupt literal percent escapes.
func backgroundTaskID(w http.ResponseWriter, r *http.Request) (string, bool) {
	taskID := chi.URLParam(r, "taskId")
	if r.URL.RawPath == "" {
		return taskID, true
	}
	taskID, err := url.PathUnescape(taskID)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"BACKGROUND_TASK_ID_INVALID", "background task identifier is invalid", nil)
		return "", false
	}
	return taskID, true
}

func backgroundTaskResponse(reading chatsvc.BackgroundTaskSnapshot) BackgroundTasksResponse {
	out := BackgroundTasksResponse{
		SessionID: reading.SessionID, ControllerGeneration: reading.Generation,
		Availability: reading.Availability, ObservedAt: reading.ObservedAt,
		Tasks: make([]BackgroundTaskResponse, 0, len(reading.Tasks)),
	}
	for _, task := range reading.Tasks {
		out.Tasks = append(out.Tasks, BackgroundTaskResponse{
			ID: task.ID, RunID: task.RunID, Kind: task.Kind, Description: task.Description, Command: task.Command, State: task.State,
			StartedAt: task.StartedAt, EndedAt: task.EndedAt, Stoppable: task.Stoppable,
		})
	}
	return out
}
