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

type hibernationSettingsService struct {
	SettingsService
	enabled bool
	calls   int
}

func (s *hibernationSettingsService) SetChatHibernationEnabled(_ context.Context, enabled bool) (settingssvc.Snapshot, error) {
	s.enabled = enabled
	s.calls++
	return settingssvc.Snapshot{ChatHibernationEnabled: enabled}, nil
}
func (s *hibernationSettingsService) ChatHarnesses([]domain.AgentHarness) []domain.AgentHarness {
	return nil
}
func (s *hibernationSettingsService) Offering() settingssvc.Offering { return settingssvc.Offering{} }

func TestSettingsChatHibernationRequiresBoolean(t *testing.T) {
	svc := &hibernationSettingsService{}
	c := &SettingsController{Svc: svc}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"true"}`} {
		w := httptest.NewRecorder()
		c.setChatHibernation(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	for _, enabled := range []bool{true, false} {
		body := `{"enabled":false}`
		if enabled {
			body = `{"enabled":true}`
		}
		w := httptest.NewRecorder()
		c.setChatHibernation(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/chat-hibernation", strings.NewReader(body)))
		var got SettingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.ChatHibernationEnabled != enabled {
			t.Fatalf("body=%s status=%d response=%+v err=%v", body, w.Code, got, err)
		}
	}
}

type versionRefreshSettingsService struct {
	hibernationSettingsService
	refresh bool
}

func (s *versionRefreshSettingsService) SetVersionRefresh(_ context.Context, enabled bool) (settingssvc.Snapshot, error) {
	s.refresh = enabled
	s.calls++
	return settingssvc.Snapshot{VersionRefresh: enabled}, nil
}

func TestSettingsVersionRefreshRequiresBoolean(t *testing.T) {
	svc := &versionRefreshSettingsService{}
	c := &SettingsController{Svc: svc}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`} {
		w := httptest.NewRecorder()
		c.setVersionRefresh(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/version-refresh", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	w := httptest.NewRecorder()
	c.setVersionRefresh(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/version-refresh", strings.NewReader(`{"enabled":false}`)))
	var got SettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.VersionRefresh || svc.refresh {
		t.Fatalf("status=%d response=%+v err=%v", w.Code, got, err)
	}
}

type computerUseSettingsService struct {
	hibernationSettingsService
	computerUse bool
}

func (s *computerUseSettingsService) SetComputerUse(_ context.Context, enabled bool) (settingssvc.Snapshot, error) {
	s.computerUse = enabled
	s.calls++
	return settingssvc.Snapshot{ComputerUse: enabled}, nil
}

func TestSettingsComputerUseRequiresBoolean(t *testing.T) {
	svc := &computerUseSettingsService{}
	c := &SettingsController{Svc: svc}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"true"}`} {
		w := httptest.NewRecorder()
		c.setComputerUse(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/computer-use", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, w.Code, svc.calls)
		}
	}
	w := httptest.NewRecorder()
	c.setComputerUse(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/computer-use", strings.NewReader(`{"enabled":true}`)))
	var got SettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || !got.ComputerUse || !svc.computerUse {
		t.Fatalf("status=%d response=%+v err=%v", w.Code, got, err)
	}
}
