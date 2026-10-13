package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

type harnessDefaultSettingsService struct {
	hibernationSettingsService
	defaults map[string]domain.HarnessDefault
}

func (s *harnessDefaultSettingsService) Get(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func (s *harnessDefaultSettingsService) SetHarnessDefault(_ context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (settingssvc.Snapshot, error) {
	s.defaults[string(agent)] = value
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func TestSettingsHarnessDefaultPatchKeepsOmittedParts(t *testing.T) {
	svc := &harnessDefaultSettingsService{defaults: map[string]domain.HarnessDefault{
		"codex": {Model: "gpt-5", Effort: "high"},
	}}
	c := &SettingsController{Svc: svc}
	patch := func(body string) int {
		router := chi.NewRouter()
		router.Patch("/api/v1/settings/harness-defaults/{agent}", c.setHarnessDefault)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/harness-defaults/codex", strings.NewReader(body)))
		return w.Code
	}
	for _, body := range []string{`{}`, `{"effort":"low"}`} {
		if code := patch(body); code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, code)
		}
	}
	if code := patch(`{"enabledModels":["gpt-5","gpt-5-mini"]}`); code != http.StatusOK {
		t.Fatalf("enabled models status=%d", code)
	}
	want := domain.HarnessDefault{Model: "gpt-5", Effort: "high", EnabledModels: []string{"gpt-5", "gpt-5-mini"}}
	if got := svc.defaults["codex"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after enabled models = %+v", got)
	}
	if code := patch(`{"model":""}`); code != http.StatusOK {
		t.Fatalf("reset model status=%d", code)
	}
	want = domain.HarnessDefault{EnabledModels: []string{"gpt-5", "gpt-5-mini"}}
	if got := svc.defaults["codex"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after model reset = %+v", got)
	}
}
