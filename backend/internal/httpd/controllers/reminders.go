package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	remindersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/reminder"
)

// ReminderService is the controller-facing reminder contract.
type ReminderService interface {
	Create(context.Context, remindersvc.CreateInput) (domain.Reminder, error)
	ListSession(context.Context, domain.SessionID) ([]domain.Reminder, error)
	Cancel(context.Context, domain.ReminderID, domain.SessionID) (domain.Reminder, error)
}

// RemindersController owns the /reminders routes.
type RemindersController struct{ Svc ReminderService }

// Register mounts reminder routes on r.
func (c *RemindersController) Register(r chi.Router) {
	r.Get("/reminders", c.list)
	r.Post("/reminders", c.create)
	r.Delete("/reminders/{reminderId}", c.cancel)
}

func (c *RemindersController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/reminders")
		return
	}
	records, err := c.Svc.ListSession(r.Context(), domain.SessionID(r.URL.Query().Get("sessionId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	response := ListRemindersResponse{Reminders: make([]ReminderResponse, len(records))}
	for i, record := range records {
		response.Reminders[i] = reminderResponse(record)
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *RemindersController) create(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/reminders")
		return
	}
	var req CreateReminderRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	rec, err := c.Svc.Create(r.Context(), remindersvc.CreateInput{
		SessionID: domain.SessionID(req.SessionID), Name: req.Name, Message: req.Message,
		DueAt: req.DueAt, CreatedBy: domain.ReminderCreator(req.CreatedBy),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ReminderEnvelope{Reminder: reminderResponse(rec)})
}

func (c *RemindersController) cancel(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/reminders/{reminderId}")
		return
	}
	id, err := url.PathUnescape(chi.URLParam(r, "reminderId"))
	if err != nil || id == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "REMINDER_ID_INVALID", "Invalid reminder id", nil)
		return
	}
	rec, err := c.Svc.Cancel(r.Context(), domain.ReminderID(id), domain.SessionID(r.URL.Query().Get("sessionId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReminderEnvelope{Reminder: reminderResponse(rec)})
}

func reminderResponse(r domain.Reminder) ReminderResponse {
	return ReminderResponse{
		ID: string(r.ID), SessionID: string(r.SessionID), ProjectID: string(r.ProjectID),
		Name: r.Name, Message: r.Message, DueAt: r.DueAt, CreatedBy: string(r.CreatedBy),
		Status: string(r.Status), StatusReason: r.StatusReason, CreatedAt: r.CreatedAt, ResolvedAt: r.ResolvedAt,
	}
}
