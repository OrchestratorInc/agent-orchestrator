package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type backgroundTasksService struct {
	fakeConversationService
	reading chatsvc.BackgroundTaskSnapshot
	err     error
	project domain.ProjectID
}

func (s *backgroundTasksService) BackgroundTasks(context.Context, domain.SessionID) (chatsvc.BackgroundTaskSnapshot, error) {
	return s.reading, s.err
}
func (s *backgroundTasksService) ListBackgroundTasks(_ context.Context, project domain.ProjectID) ([]chatsvc.BackgroundTaskSnapshot, error) {
	s.project = project
	return []chatsvc.BackgroundTaskSnapshot{s.reading}, s.err
}

func TestBackgroundTasksRoutesPreserveAvailabilityAndErrors(t *testing.T) {
	svc := &backgroundTasksService{reading: chatsvc.BackgroundTaskSnapshot{SessionID: "s", Availability: "unavailable"}}
	router := chi.NewRouter()
	(&controllers.ConversationsController{Svc: svc}).Register(router)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	r := request("/sessions/s/background-tasks")
	var reading controllers.BackgroundTasksResponse
	if err := json.Unmarshal(r.Body.Bytes(), &reading); err != nil || r.Code != 200 || reading.Availability != "unavailable" || reading.Tasks == nil {
		t.Fatalf("response=%s status=%d err=%v", r.Body, r.Code, err)
	}
	r = request("/sessions/background-tasks?projectId=p")
	if r.Code != 200 || svc.project != "p" {
		t.Fatalf("batch=%d project=%q", r.Code, svc.project)
	}
	svc.err = ports.ErrSessionNotFound
	r = request("/sessions/missing/background-tasks")
	if r.Code != 404 {
		t.Fatalf("missing session=%d %s", r.Code, r.Body)
	}
}
