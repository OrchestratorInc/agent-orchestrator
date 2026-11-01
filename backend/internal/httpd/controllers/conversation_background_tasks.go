package controllers

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type backgroundTasksService interface {
	BackgroundTasks(context.Context, domain.SessionID) (chatsvc.BackgroundTaskSnapshot, error)
	ListBackgroundTasks(context.Context, domain.ProjectID) ([]chatsvc.BackgroundTaskSnapshot, error)
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

func backgroundTaskResponse(reading chatsvc.BackgroundTaskSnapshot) BackgroundTasksResponse {
	out := BackgroundTasksResponse{
		SessionID: reading.SessionID, ControllerGeneration: reading.Generation,
		Availability: reading.Availability, ObservedAt: reading.ObservedAt,
		Tasks: make([]BackgroundTaskResponse, 0, len(reading.Tasks)),
	}
	for _, task := range reading.Tasks {
		out.Tasks = append(out.Tasks, BackgroundTaskResponse{
			ID: task.ID, RunID: task.RunID, Kind: task.Kind, Description: task.Description, State: task.State, EndedAt: task.EndedAt,
		})
	}
	return out
}
