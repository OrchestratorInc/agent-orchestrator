package controllers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func skillsRequest(t *testing.T, svc controllers.ConversationService) (int, []byte) {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.ConversationsController{Svc: svc}).Register(r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/p1-1/conversation/skills", nil))
	return rec.Code, rec.Body.Bytes()
}

func decodeSkills(t *testing.T, body []byte) controllers.ConversationSkillsResponse {
	t.Helper()
	var out controllers.ConversationSkillsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func TestSkillsRouteReturnsTheProvidersList(t *testing.T) {
	status, body := skillsRequest(t, &fakeConversationService{
		skills: []ports.ChatSkill{
			{Name: "review", DisplayName: "Review", Description: "Look at the diff", InputHint: "<branch>", Source: "repo"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", status, body)
	}
	got := decodeSkills(t, body)
	if len(got.Skills) != 1 {
		t.Fatalf("skills = %+v, want 1", got.Skills)
	}
	if got.Skills[0] != (controllers.ConversationSkillResponse{
		Name: "review", DisplayName: "Review", Description: "Look at the diff", InputHint: "<branch>", Source: "repo",
	}) {
		t.Errorf("skill = %+v", got.Skills[0])
	}
}

func TestSkillsRouteAnswersEmptyWhenTheDriverCannotList(t *testing.T) {
	status, body := skillsRequest(t, &fakeConversationService{skillErr: chatsvc.ErrSkillsUnsupported})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", status, body)
	}
	got := decodeSkills(t, body)
	if got.Skills == nil {
		t.Fatal("skills is null; an absent catalog must encode as [] so a client can iterate it")
	}
	if len(got.Skills) != 0 {
		t.Errorf("skills = %+v, want empty", got.Skills)
	}
}

func TestSkillsRouteReportsAMissingController(t *testing.T) {
	status, body := skillsRequest(t, &fakeConversationService{skillErr: chatsvc.ErrNoController})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", status, body)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.Code != "" && envelope.Code != "CHAT_CONTROLLER_NOT_READY" {
		t.Errorf("code = %q", envelope.Code)
	}
}

func TestSkillsRouteIsNotImplementedWithoutAService(t *testing.T) {
	status, _ := skillsRequest(t, nil)
	if status != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", status)
	}
}
