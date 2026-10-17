package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

type cardSummariesSettingsService struct {
	SettingsService
	calls int
}

func (s *cardSummariesSettingsService) SetCardSummaries(_ context.Context, enabled bool) (settingssvc.Snapshot, error) {
	s.calls++
	return settingssvc.Snapshot{CardSummaries: enabled}, nil
}
func (*cardSummariesSettingsService) ChatHarnesses([]domain.AgentHarness) []domain.AgentHarness {
	return nil
}
func (*cardSummariesSettingsService) Offering() settingssvc.Offering { return settingssvc.Offering{} }

func TestSettingsCardSummariesRequiresBooleanAndReportsTheValue(t *testing.T) {
	svc := &cardSummariesSettingsService{}
	c := &SettingsController{Svc: svc}
	patch := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c.setCardSummaries(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/card-summaries", strings.NewReader(body)))
		return w
	}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"true"}`} {
		if w := patch(body); w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	for body, want := range map[string]bool{`{"enabled":true}`: true, `{"enabled":false}`: false} {
		w := patch(body)
		var got SettingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.CardSummaries != want {
			t.Fatalf("body=%s status=%d response=%+v err=%v", body, w.Code, got, err)
		}
	}
}
