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
	if agent != domain.HarnessCodex {
		return settingssvc.Snapshot{}, settingssvc.ErrUnknownHarness
	}
	s.defaults[string(agent)] = value
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func TestSettingsHarnessDefaultPatchRequiresDisabledModels(t *testing.T) {
	svc := &harnessDefaultSettingsService{defaults: map[string]domain.HarnessDefault{}}
	c := &SettingsController{Svc: svc}
	patch := func(agent, body string) int {
		router := chi.NewRouter()
		router.Patch("/api/v1/settings/harness-defaults/{agent}", c.setHarnessDefault)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/harness-defaults/"+agent, strings.NewReader(body)))
		return w.Code
	}
	for _, body := range []string{`{}`, `{"model":"gpt-5","effort":"low"}`} {
		if code := patch("codex", body); code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, code)
		}
	}
	if code := patch("not-an-agent", `{"disabledModels":[]}`); code != http.StatusNotFound {
		t.Fatalf("unknown harness status=%d", code)
	}
	if code := patch("codex", `{"disabledModels":["gpt-5","gpt-5-mini"]}`); code != http.StatusOK {
		t.Fatalf("disabled models status=%d", code)
	}
	want := domain.HarnessDefault{DisabledModels: []string{"gpt-5", "gpt-5-mini"}}
	if got := svc.defaults["codex"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after disabled models = %+v", got)
	}
}
