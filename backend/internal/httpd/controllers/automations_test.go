package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	automationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/automation"
)

type fakeAutomationService struct {
	created automationsvc.CreateInput
	updated automationsvc.UpdateInput
	items   []domain.Automation
	deleted domain.AutomationID
}

func (f *fakeAutomationService) Create(_ context.Context, input automationsvc.CreateInput) (domain.Automation, error) {
	f.created = input
	return domain.Automation{ID: "automation-1", ProjectID: input.ProjectID, DisplayName: input.DisplayName, Prompt: input.Prompt, TemplateID: input.TemplateID, UseCase: input.UseCase, Kind: input.Kind, Timezone: input.Timezone, Enabled: true, NextRunAt: time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)}, nil
}
func (f *fakeAutomationService) Get(_ context.Context, id domain.AutomationID) (domain.Automation, error) {
	return f.items[0], nil
}
func (f *fakeAutomationService) List(_ context.Context, filter domain.AutomationFilter) (domain.AutomationPage, error) {
	return domain.AutomationPage{Items: f.items, Total: int64(len(f.items))}, nil
}
func (f *fakeAutomationService) Update(_ context.Context, id domain.AutomationID, input automationsvc.UpdateInput) (domain.Automation, error) {
	f.updated = input
	return f.items[0], nil
}
func (f *fakeAutomationService) Delete(_ context.Context, id domain.AutomationID) error {
	f.deleted = id
	return nil
}
func (f *fakeAutomationService) Runs(_ context.Context, filter domain.AutomationRunFilter) (domain.AutomationRunPage, error) {
	return domain.AutomationRunPage{}, nil
}

func automationTestRouter(service AutomationService) http.Handler {
	r := chi.NewRouter()
	(&AutomationsController{Svc: service}).Register(r)
	return r
}

// Request decoding must preserve the schedule choice and route through the
// automation service rather than launching a session from the controller.
func TestAutomationsCreateReturnsDefinition(t *testing.T) {
	service := &fakeAutomationService{}
	body := `{"projectId":"demo","displayName":"Morning","prompt":"Review","kind":"worker","cron":"0 9 * * *","timezone":"Asia/Calcutta","templateId":"daily_pr_review_v1","useCase":"pr_review"}`
	req := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(body))
	rec := httptest.NewRecorder()
	automationTestRouter(service).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.created.TemplateID != "daily_pr_review_v1" || service.created.UseCase != "pr_review" {
		t.Fatalf("metadata missing: %+v", service.created)
	}
	if service.created.ProjectID != "demo" || service.created.Cron != "0 9 * * *" || service.created.Timezone != "Asia/Calcutta" {
		t.Fatalf("create input = %#v", service.created)
	}
	var response AutomationEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Automation.ID != "automation-1" || response.Automation.TemplateID != "daily_pr_review_v1" || response.Automation.UseCase != "pr_review" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestAutomationsDeleteReturnsNoContent(t *testing.T) {
	service := &fakeAutomationService{}
	req := httptest.NewRequest(http.MethodDelete, "/automations/automation-1", nil)
	rec := httptest.NewRecorder()
	automationTestRouter(service).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || service.deleted != "automation-1" {
		t.Fatalf("status=%d deleted=%q body=%s", rec.Code, service.deleted, rec.Body.String())
	}
}

func TestAutomationUpdatePurposeAndRejectOriginChange(t *testing.T) {
	service := &fakeAutomationService{items: []domain.Automation{{ID: "automation-1"}}}
	router := automationTestRouter(service)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"useCase":"other"}`, http.StatusOK},
		{`{"templateId":"custom"}`, http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/automations/automation-1", strings.NewReader(tc.body)))
		if rec.Code != tc.status {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if service.updated.UseCase == nil || *service.updated.UseCase != "other" {
		t.Fatalf("update input=%+v", service.updated)
	}
}
