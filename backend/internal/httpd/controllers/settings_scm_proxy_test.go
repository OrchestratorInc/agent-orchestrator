package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

type scmProxySettingsService struct {
	hibernationSettingsService
	url   string
	calls int
}

func (s *scmProxySettingsService) SetScmProxyURL(_ context.Context, url string) (settingssvc.Snapshot, error) {
	s.calls++
	if strings.HasPrefix(url, "bad://") {
		return settingssvc.Snapshot{}, fmt.Errorf("%w: %q", settingssvc.ErrInvalidScmProxyURL, url)
	}
	s.url = url
	return settingssvc.Snapshot{ScmProxyURL: url}, nil
}

func TestSettingsScmProxyRoundTrip(t *testing.T) {
	svc := &scmProxySettingsService{}
	c := &SettingsController{Svc: svc}

	w := httptest.NewRecorder()
	c.setScmProxy(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/scm-proxy", strings.NewReader(`{"url":"socks5h://127.0.0.1:10808"}`)))
	var got SettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.ScmProxyURL != "socks5h://127.0.0.1:10808" || svc.url != "socks5h://127.0.0.1:10808" {
		t.Fatalf("status=%d response=%+v stored=%q err=%v", w.Code, got, svc.url, err)
	}

	// Empty clears the override.
	w = httptest.NewRecorder()
	c.setScmProxy(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/scm-proxy", strings.NewReader(`{"url":""}`)))
	got = SettingsResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.ScmProxyURL != "" || svc.url != "" {
		t.Fatalf("clear: status=%d response=%+v stored=%q err=%v", w.Code, got, svc.url, err)
	}
}

func TestSettingsScmProxyRejectsInvalidURL(t *testing.T) {
	svc := &scmProxySettingsService{}
	c := &SettingsController{Svc: svc}
	w := httptest.NewRecorder()
	c.setScmProxy(w, httptest.NewRequest(http.MethodPatch, "/api/v1/settings/scm-proxy", strings.NewReader(`{"url":"bad://nope"}`)))
	if w.Code != http.StatusBadRequest || svc.calls != 1 {
		t.Fatalf("status=%d calls=%d, want 400 after one service call", w.Code, svc.calls)
	}
	if !strings.Contains(w.Body.String(), "SCM_PROXY_INVALID") {
		t.Fatalf("body missing SCM_PROXY_INVALID: %s", w.Body.String())
	}
}
