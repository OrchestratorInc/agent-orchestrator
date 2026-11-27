package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/computeruse"
)

type timelineComputerUse struct {
	ComputerUse
	entries     []computeruse.TimelineEntry
	screenshots map[string]string
	gotSession  string
	target      string
}

func (f *timelineComputerUse) Timeline(_ context.Context, sessionID string) ([]computeruse.TimelineEntry, error) {
	f.gotSession = sessionID
	return f.entries, nil
}

func (f *timelineComputerUse) SessionTarget(_ context.Context, sessionID string) (string, error) {
	f.gotSession = sessionID
	return f.target, nil
}

func (f *timelineComputerUse) ScreenshotPath(_ context.Context, _ string, name string) (string, error) {
	if path, ok := f.screenshots[name]; ok {
		return path, nil
	}
	return "", apierr.NotFound("COMPUTER_USE_SCREENSHOT_NOT_FOUND", "The screenshot is no longer available.")
}

func computerUseRouter(svc ComputerUse) http.Handler {
	r := chi.NewRouter()
	(&ComputerUseController{Svc: svc}).Register(r)
	return r
}

func TestComputerUseTimelineListsEntries(t *testing.T) {
	svc := &timelineComputerUse{entries: []computeruse.TimelineEntry{{
		ID: "1", At: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC), Tool: "click", OK: true, Screenshots: []string{"a.png"},
	}}}
	recorder := httptest.NewRecorder()
	computerUseRouter(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/computer-use/sessions/ao-1/timeline", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body ComputerUseTimelineResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if svc.gotSession != "ao-1" || len(body.Entries) != 1 || body.Entries[0].Tool != "click" || body.Entries[0].Screenshots[0] != "a.png" {
		t.Fatalf("session = %q, body = %+v", svc.gotSession, body)
	}
}

func TestComputerUseScreenshotServesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := computerUseRouter(&timelineComputerUse{screenshots: map[string]string{"shot.png": path}})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/computer-use/sessions/ao-1/screenshots/shot.png", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != string(png) {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/computer-use/sessions/ao-1/screenshots/gone.png", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing screenshot status = %d", recorder.Code)
	}
}

func TestComputerUseSessionTargetReportsWhereTheChatRuns(t *testing.T) {
	svc := &timelineComputerUse{target: "host-mac"}
	recorder := httptest.NewRecorder()
	computerUseRouter(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/computer-use/sessions/ao-1/target", nil))
	if recorder.Code != http.StatusOK || svc.gotSession != "ao-1" || !strings.Contains(recorder.Body.String(), `"target":"host-mac"`) {
		t.Fatalf("status = %d, session = %q, body = %s", recorder.Code, svc.gotSession, recorder.Body)
	}
}
