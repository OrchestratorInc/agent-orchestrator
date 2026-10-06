package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type renderStub struct {
	*fakeConversationService
	input  chatsvc.RenderInput
	result chatsvc.RenderResult
	err    error
}

func (s *renderStub) PublishRender(_ context.Context, _ domain.SessionID, in chatsvc.RenderInput) (chatsvc.RenderResult, error) {
	s.input = in
	return s.result, s.err
}

func renderRouter(t *testing.T, dataDir string, svc *renderStub) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{DataDir: dataDir}, log, nil, httpd.APIDeps{
		Sessions:      newFakeSessionService(),
		Conversations: svc,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRenderRouteServesTheStoredPageSandboxed(t *testing.T) {
	dir := t.TempDir()
	if err := attachmentstore.New(dir).PutRender(context.Background(), "proj-1", "r1", []byte("<p>chart</p>")); err != nil {
		t.Fatal(err)
	}
	srv := renderRouter(t, dir, &renderStub{fakeConversationService: &fakeConversationService{}})

	resp, err := http.Get(srv.URL + "/api/v1/sessions/proj-1/renders/r1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "<p>chart</p>" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": "sandbox allow-scripts allow-forms",
		"Content-Type":            "text/html; charset=utf-8",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// The page's own scripts run with an opaque origin; the daemon refuses it,
	// even for a "simple" no-cors POST that would otherwise reach a handler.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/proj-1/renders",
		strings.NewReader(`{"html":"<p>x</p>","title":"x"}`))
	req.Header.Set("Origin", "null")
	req.Header.Set("Content-Type", "text/plain")
	refused, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Fatalf("Origin: null publish = %d, want 403", refused.StatusCode)
	}

	missing, err := http.Get(srv.URL + "/api/v1/sessions/proj-1/renders/..%2Fattachment-a.png")
	if err != nil {
		t.Fatal(err)
	}
	_ = missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("traversal-shaped id = %d, want 404", missing.StatusCode)
	}
}

func TestPublishRenderRouteMapsOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"created", nil, http.StatusCreated, ""},
		{"invalid", fmt.Errorf("%w: the page is empty", chatsvc.ErrRenderInvalid), http.StatusBadRequest, "RENDER_INVALID"},
		{"no turn", chatsvc.ErrNoActiveTurn, http.StatusConflict, "RENDER_NO_ACTIVE_TURN"},
		{"tui session", chatsvc.ErrNotChatMode, http.StatusConflict, "SESSION_MODE_MISMATCH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &renderStub{
				fakeConversationService: &fakeConversationService{},
				result:                  chatsvc.RenderResult{RenderID: "r1", ActivityID: "a1", Path: "/api/v1/sessions/proj-1/renders/r1"},
				err:                     tc.err,
			}
			srv := renderRouter(t, t.TempDir(), svc)
			resp, err := http.Post(srv.URL+"/api/v1/sessions/proj-1/renders", "application/json",
				strings.NewReader(`{"html":"<p>x</p>","title":"Chart","height":420}`))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			var body struct {
				Code     string `json:"code"`
				RenderID string `json:"renderId"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if resp.StatusCode != tc.status || body.Code != tc.code {
				t.Fatalf("status=%d code=%q, want %d %q", resp.StatusCode, body.Code, tc.status, tc.code)
			}
			if tc.err == nil && (body.RenderID != "r1" || svc.input != (chatsvc.RenderInput{HTML: "<p>x</p>", Title: "Chart", Height: 420})) {
				t.Fatalf("renderId=%q input=%+v", body.RenderID, svc.input)
			}
		})
	}
}

type renderCheckStub struct {
	*renderStub
	checkInput chatsvc.RenderCheckInput
	checkErr   error
}

func (s *renderCheckStub) CheckRender(_ context.Context, _ domain.SessionID, in chatsvc.RenderCheckInput) (chatsvc.RenderCheckResult, error) {
	s.checkInput = in
	return chatsvc.RenderCheckResult{PNG: "iVBORw0KGgo=", Width: 720, Height: 412, ContentHeight: 412,
		ConsoleMessages: []chatsvc.RenderConsoleMessage{{Level: "error", Text: "boom"}}}, s.checkErr
}

func TestRenderCheckRouteReturnsTheScreenshotAndNamesTheOrigin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"ok", nil, http.StatusOK, ""},
		{"no desktop app", chatsvc.ErrRenderCheckUnavailable, http.StatusServiceUnavailable, "RENDER_CHECK_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &renderCheckStub{renderStub: &renderStub{fakeConversationService: &fakeConversationService{}}, checkErr: tc.err}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{DataDir: t.TempDir()}, log, nil, httpd.APIDeps{
				Sessions: newFakeSessionService(), Conversations: svc,
			}, httpd.ControlDeps{}))
			t.Cleanup(srv.Close)

			resp, err := http.Post(srv.URL+"/api/v1/sessions/proj-1/renders/check", "application/json",
				strings.NewReader(`{"html":"<p>x</p>","width":390}`))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			var body struct {
				Code       string `json:"code"`
				Screenshot struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"screenshot"`
				ContentHeight int `json:"contentHeight"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if resp.StatusCode != tc.status || body.Code != tc.code {
				t.Fatalf("status=%d code=%q, want %d %q", resp.StatusCode, body.Code, tc.status, tc.code)
			}
			if tc.err != nil {
				return
			}
			if body.Screenshot.MimeType != "image/png" || body.Screenshot.Data != "iVBORw0KGgo=" || body.ContentHeight != 412 {
				t.Fatalf("body = %+v", body)
			}
			if svc.checkInput.Width != 390 || svc.checkInput.BaseURL != srv.URL {
				t.Fatalf("input = %+v", svc.checkInput)
			}
		})
	}
}
