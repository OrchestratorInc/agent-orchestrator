package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestDesktopExitShutdownPolicy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		owner       string
		replacement bool
		wantStop    bool
	}{
		{name: "desktop death stops agents", owner: "app", wantStop: true},
		{name: "planned replacement preserves agents", owner: "app", replacement: true},
		{name: "headless daemon preserves agents"},
		{name: "keep alive daemon preserves agents", owner: "persistent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_OWNER", tc.owner)
			srv := &Server{cfg: config.Config{AppRunID: "run-1"}, shutdownRequested: make(chan struct{})}
			if tc.replacement {
				srv.RequestShutdown()
			}
			srv.RequestShutdownAfterDesktopExit()
			srv.RequestShutdownAfterDesktopExit()
			if got := srv.StopAgentsRequested(); got != tc.wantStop {
				t.Fatalf("stop agents = %v, want %v", got, tc.wantStop)
			}
			select {
			case <-srv.shutdownRequested:
			default:
				t.Fatal("desktop exit did not request daemon shutdown")
			}
		})
	}
}

func TestDesktopQuitRequiresCurrentAppOwner(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
		runID string
		want  bool
	}{
		{name: "current desktop", owner: "app", runID: "run-1", want: true},
		{name: "stale desktop", owner: "app", runID: "old-run"},
		{name: "missing desktop identity", owner: "app"},
		{name: "keep alive daemon", owner: "persistent", runID: "run-1"},
		{name: "headless daemon", runID: "run-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_OWNER", tc.owner)
			srv := &Server{cfg: config.Config{AppRunID: "run-1"}, shutdownRequested: make(chan struct{})}
			if got := srv.requestAgentShutdown(tc.runID); got != tc.want {
				t.Fatalf("accepted = %v, want %v", got, tc.want)
			}
			if got := srv.StopAgentsRequested(); got != tc.want {
				t.Fatalf("stop agents = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDesktopShutdownEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		origin     string
		accept     bool
		wantStatus int
		wantStop   bool
	}{
		{name: "plain shutdown preserves agents", wantStatus: http.StatusAccepted},
		{name: "explicit quit stops agents", body: `{"stopAgents":true,"appRunId":"run-1"}`, accept: true, wantStatus: http.StatusAccepted, wantStop: true},
		{name: "replacement preserves agents", body: `{"stopAgents":false,"appRunId":"run-1"}`, wantStatus: http.StatusAccepted},
		{name: "wrong owner cannot shut down", body: `{"stopAgents":true,"appRunId":"old-run"}`, wantStatus: http.StatusConflict},
		{name: "invalid request", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "browser cannot request cleanup", body: `{"stopAgents":true,"appRunId":"run-1"}`, origin: "https://example.com", wantStatus: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shutdown := make(chan struct{}, 1)
			stopRequested := false
			router := chi.NewRouter()
			mountControl(router, ControlDeps{
				RequestShutdown: func() { shutdown <- struct{}{} },
				RequestAgentShutdown: func(runID string) bool {
					stopRequested = tc.accept && runID == "run-1"
					return stopRequested
				},
			})
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/shutdown", strings.NewReader(tc.body))
			req.RemoteAddr = "127.0.0.1:12345"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tc.wantStatus || stopRequested != tc.wantStop {
				t.Fatalf("status=%d stop=%v, want status=%d stop=%v", response.Code, stopRequested, tc.wantStatus, tc.wantStop)
			}
			if response.Code == http.StatusAccepted {
				select {
				case <-shutdown:
				case <-time.After(time.Second):
					t.Fatal("accepted request did not trigger shutdown")
				}
			}
		})
	}
}
