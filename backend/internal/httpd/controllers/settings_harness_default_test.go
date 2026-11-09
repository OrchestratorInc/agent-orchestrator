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

func (s *harnessDefaultSettingsService) PatchHarnessDefault(_ context.Context, agent domain.AgentHarness, patch settingssvc.HarnessDefaultPatch) (settingssvc.Snapshot, error) {
	if agent != domain.HarnessCodex {
		return settingssvc.Snapshot{}, settingssvc.ErrUnknownHarness
	}
	value := s.defaults[string(agent)]
	if patch.DisabledModels != nil {
		value.DisabledModels = *patch.DisabledModels
	}
	if patch.ExecutableDirectory != nil {
		value.ExecutableDirectory = *patch.ExecutableDirectory
	}
	if patch.ExecutablePath != nil {
		value.ExecutablePath = *patch.ExecutablePath
	}
	s.defaults[string(agent)] = value
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func TestSettingsHarnessDefaultPatchRequiresPreference(t *testing.T) {
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
	if code := patch("codex", `{"executableDirectory":"/desktop","executablePath":"/desktop/codex"}`); code != http.StatusOK {
		t.Fatalf("CLI-only patch status=%d", code)
	}
	want.ExecutableDirectory, want.ExecutablePath = "/desktop", "/desktop/codex"
	if got := svc.defaults["codex"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI patch lost omitted model fields: %+v", got)
	}
	if code := patch("codex", `{"executableDirectory":"","executablePath":""}`); code != http.StatusOK {
		t.Fatalf("reset CLI status=%d", code)
	}
	if got := svc.defaults["codex"]; got.ExecutablePath != "" || !reflect.DeepEqual(got.DisabledModels, want.DisabledModels) {
		t.Fatalf("reset lost model fields: %+v", got)
	}
}
