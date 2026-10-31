package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	remindersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/reminder"
)

type fakeReminderService struct {
	input         remindersvc.CreateInput
	listSession   domain.SessionID
	cancelID      domain.ReminderID
	cancelSession domain.SessionID
	err           error
	reminders     []domain.Reminder
}

var reminderDue = time.Date(2026, 10, 31, 6, 30, 0, 0, time.UTC)

func (f *fakeReminderService) Create(_ context.Context, input remindersvc.CreateInput) (domain.Reminder, error) {
	f.input = input
	if f.err != nil {
		return domain.Reminder{}, f.err
	}
	return domain.Reminder{ID: "rem_1", SessionID: input.SessionID, ProjectID: "ao", Name: input.Name, Message: input.Message,
		DueAt: input.DueAt, CreatedBy: domain.ReminderByAgent, Status: domain.ReminderPending, CreatedAt: reminderDue.Add(-time.Hour)}, nil
}

func (f *fakeReminderService) ListSession(_ context.Context, id domain.SessionID) ([]domain.Reminder, error) {
	f.listSession = id
	return f.reminders, f.err
}

func (f *fakeReminderService) Cancel(_ context.Context, id domain.ReminderID, session domain.SessionID) (domain.Reminder, error) {
	f.cancelID, f.cancelSession = id, session
	if f.err != nil {
		return domain.Reminder{}, f.err
	}
	return domain.Reminder{ID: id, SessionID: "ao-7", Status: domain.ReminderCancelled}, nil
}

func reminderServer(t *testing.T, svc *fakeReminderService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reminders: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRemindersAPI_CreateListCancel(t *testing.T) {
	svc := &fakeReminderService{}
	srv := reminderServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/reminders", `{"sessionId":"ao-7","message":"check CI","name":"CI","dueAt":"2026-10-31T06:30:00Z"}`)
	if status != http.StatusCreated || svc.input.SessionID != "ao-7" || svc.input.Message != "check CI" || svc.input.Name != "CI" || !svc.input.DueAt.Equal(reminderDue) {
		t.Fatalf("status=%d body=%s input=%+v", status, body, svc.input)
	}
	if !strings.Contains(string(body), `"reminder":{"id":"rem_1"`) || !strings.Contains(string(body), `"status":"pending"`) {
		t.Fatalf("create body = %s", body)
	}

	svc.reminders = []domain.Reminder{{ID: "rem_1", SessionID: "ao-7", Message: "check CI", DueAt: reminderDue, Status: domain.ReminderPending}}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/reminders?sessionId=ao-7", "")
	if status != http.StatusOK || svc.listSession != "ao-7" || !strings.Contains(string(body), `"reminders":[{"id":"rem_1"`) {
		t.Fatalf("list status=%d body=%s", status, body)
	}

	body, status, _ = doRequest(t, srv, "DELETE", "/api/v1/reminders/rem_1?sessionId=ao-7", "")
	if status != http.StatusOK || svc.cancelID != "rem_1" || svc.cancelSession != "ao-7" || !strings.Contains(string(body), `"status":"cancelled"`) {
		t.Fatalf("cancel status=%d body=%s", status, body)
	}
}

func TestRemindersAPI_Errors(t *testing.T) {
	svc := &fakeReminderService{}
	srv := reminderServer(t, svc)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/reminders", `{"sessionId":"ao-7","unknown":true}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", status)
	}
	svc.err = apierr.Conflict("REMINDER_LIMIT", "too many", nil)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/reminders", `{"sessionId":"ao-7","message":"x","dueAt":"2026-10-31T06:30:00Z"}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "REMINDER_LIMIT") || !strings.Contains(string(body), "requestId") {
		t.Fatalf("status=%d body=%s", status, body)
	}
	svc.err = apierr.NotFound("REMINDER_NOT_FOUND", "Unknown reminder")
	if body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/reminders/rem_9", ""); status != http.StatusNotFound || !strings.Contains(string(body), "REMINDER_NOT_FOUND") {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestRemindersAPI_NotImplementedWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	defer srv.Close()
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/reminders?sessionId=x", ""); status != http.StatusNotImplemented {
		t.Fatalf("status=%d", status)
	}
}
