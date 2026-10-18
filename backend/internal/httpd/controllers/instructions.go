package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	instructionssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/instructions"
)

// InstructionsService is the controller-facing standing-instructions contract.
type InstructionsService interface {
	Get(context.Context, domain.ProjectID) (instructionssvc.View, error)
	Save(context.Context, domain.ProjectID, instructionssvc.SaveInput) (instructionssvc.View, error)
}

// InstructionsController owns the routes for the standing instructions AO
// adds to new sessions: the user's own and each project's.
type InstructionsController struct {
	Svc InstructionsService
}

// maxInstructionsRequestBytes leaves room for JSON escaping around the 8 KiB
// text limit.
const maxInstructionsRequestBytes = 64 << 10

// Register mounts the instructions routes.
func (c *InstructionsController) Register(r chi.Router) {
	r.Get("/instructions", c.get)
	r.Put("/instructions", c.save)
	r.Get("/projects/{projectId}/instructions", c.get)
	r.Put("/projects/{projectId}/instructions", c.save)
}

func (c *InstructionsController) get(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectId"))
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, instructionsPath(projectID))
		return
	}
	view, err := c.Svc.Get(r.Context(), projectID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, InstructionsEnvelope{Instructions: instructionsResponse(view)})
}

func (c *InstructionsController) save(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectId"))
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPut, instructionsPath(projectID))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxInstructionsRequestBytes)
	var req SaveInstructionsRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	view, err := c.Svc.Save(r.Context(), projectID, instructionssvc.SaveInput{
		Body: req.Body, Revision: req.Revision, SessionID: domain.SessionID(req.SessionID),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, InstructionsEnvelope{Instructions: instructionsResponse(view)})
}

func instructionsPath(projectID domain.ProjectID) string {
	if projectID == "" {
		return "/api/v1/instructions"
	}
	return "/api/v1/projects/{projectId}/instructions"
}

func instructionsResponse(view instructionssvc.View) InstructionsResponse {
	response := InstructionsResponse{
		ProjectID: string(view.ProjectID), Body: view.Body, Revision: view.Revision,
		UpdatedAt: view.UpdatedAt, PreviousBody: view.PreviousBody,
		ProtectedLines: view.ProtectedLines, RemovedLines: view.RemovedLines,
	}
	if response.ProtectedLines == nil {
		response.ProtectedLines = []string{}
	}
	if response.RemovedLines == nil {
		response.RemovedLines = []string{}
	}
	response.History = make([]InstructionsChangeResponse, 0, len(view.History))
	for _, change := range view.History {
		response.History = append(response.History, InstructionsChangeResponse{
			At: change.At, Session: change.Session, Dreamed: change.Dreamed, Added: change.Added, Removed: change.Removed,
		})
	}
	if view.UpdatedBy != nil {
		response.UpdatedBy = &InstructionsAuthorResponse{
			SessionID: string(view.UpdatedBy.SessionID), Title: view.UpdatedBy.Title, Dreamed: view.UpdatedBy.Dreamed,
		}
	}
	return response
}
