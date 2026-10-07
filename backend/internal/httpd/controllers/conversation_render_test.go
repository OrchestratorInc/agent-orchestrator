package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/renderpage"
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
	store := attachmentstore.New(dir)
	if err := store.PutRender(context.Background(), "proj-1", "r1", []byte("<p>chart</p>")); err != nil {
		t.Fatal(err)
	}
	srv := renderRouter(t, dir, &renderStub{fakeConversationService: &fakeConversationService{}})

	resp, err := http.Get(srv.URL + "/api/v1/sessions/proj-1/renders/r1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// The bootstrap is added as the page is served; the stored file stays raw.
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<style id="ao-theme">`) || !strings.HasSuffix(string(body), "<p>chart</p>") {
		t.Fatalf("status=%d body=%.200q", resp.StatusCode, body)
	}
	file, _, err := store.OpenRender(context.Background(), "proj-1", "r1")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(file)
	_ = file.Close()
	if string(stored) != "<p>chart</p>" {
		t.Fatalf("stored file = %q, want the raw page", stored)
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": "sandbox allow-scripts allow-forms",
		"Content-Type":            "text/html; charset=utf-8",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "private, no-cache",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	// The ETag names both the bootstrap and the stored page, so a daemon
	// upgrade or a different page never revalidates to a stale document.
	etag := resp.Header.Get("ETag")
	if want := `"` + renderpage.Version + "-"; !strings.HasPrefix(etag, want) || len(etag) != len(want)+16+1 {
		t.Fatalf("ETag = %q, want %s<16 hex>\"", etag, want)
	}
	revalidate, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/proj-1/renders/r1", nil)
	revalidate.Header.Set("If-None-Match", etag)
	notModified, err := http.DefaultClient.Do(revalidate)
	if err != nil {
		t.Fatal(err)
	}
	_ = notModified.Body.Close()
	if notModified.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %s = %d, want 304", etag, notModified.StatusCode)
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

func TestRenderRouteServesTheStoredSourceAsPlainText(t *testing.T) {
	dir := t.TempDir()
	const page = "<p>chart</p><script>x()</script>"
	if err := attachmentstore.New(dir).PutRender(context.Background(), "proj-1", "r1", []byte(page)); err != nil {
		t.Fatal(err)
	}
	srv := renderRouter(t, dir, &renderStub{fakeConversationService: &fakeConversationService{}})
	get := func(query string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/v1/sessions/proj-1/renders/r1" + query)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(body)
	}

	source, body := get("?source=1")
	// The raw stored bytes, with no bootstrap.
	if source.StatusCode != http.StatusOK || body != page {
		t.Fatalf("status=%d body=%q, want the stored page", source.StatusCode, body)
	}
	for header, want := range map[string]string{
		"Content-Type":            "text/plain; charset=utf-8",
		"Content-Security-Policy": "sandbox allow-scripts allow-forms",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "private, no-cache",
	} {
		if got := source.Header.Get(header); got != want {
			t.Errorf("source %s = %q, want %q", header, got, want)
		}
	}

	// Any other value serves the page, under a tag the source's never matches.
	for _, query := range []string{"", "?source=0", "?source=true"} {
		doc, docBody := get(query)
		if doc.Header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(docBody, `<style id="ao-theme">`) {
			t.Fatalf("%q: Content-Type=%q body=%.80q, want the served page", query, doc.Header.Get("Content-Type"), docBody)
		}
		if want := `"src-` + strings.Trim(doc.Header.Get("ETag"), `"`) + `"`; source.Header.Get("ETag") != want {
			t.Fatalf("source ETag = %q, want %s", source.Header.Get("ETag"), want)
		}
	}
	revalidate, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/proj-1/renders/r1", nil)
	revalidate.Header.Set("If-None-Match", source.Header.Get("ETag"))
	full, err := http.DefaultClient.Do(revalidate)
	if err != nil {
		t.Fatal(err)
	}
	_ = full.Body.Close()
	if full.StatusCode != http.StatusOK {
		t.Fatalf("page with the source's ETag = %d, want 200", full.StatusCode)
	}
}

// A terminal session has no chat thread; the agent is pointed at ao preview instead.
const renderNeedsChatMessage = "ao render works only in chat sessions; in a terminal session, open the file with ao preview <file>"

func TestPublishRenderRouteMapsOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"created", nil, http.StatusCreated, "", ""},
		{"invalid", fmt.Errorf("%w: the page is empty", chatsvc.ErrRenderInvalid), http.StatusBadRequest, "RENDER_INVALID", ""},
		{"no turn", chatsvc.ErrNoActiveTurn, http.StatusConflict, "RENDER_NO_ACTIVE_TURN", ""},
		{"tui session", chatsvc.ErrNotChatMode, http.StatusConflict, "SESSION_MODE_MISMATCH", renderNeedsChatMessage},
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
				Message  string `json:"message"`
				RenderID string `json:"renderId"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if resp.StatusCode != tc.status || body.Code != tc.code {
				t.Fatalf("status=%d code=%q, want %d %q", resp.StatusCode, body.Code, tc.status, tc.code)
			}
			if tc.message != "" && body.Message != tc.message {
				t.Fatalf("message=%q, want %q", body.Message, tc.message)
			}
			if tc.err == nil && (body.RenderID != "r1" || svc.input != (chatsvc.RenderInput{HTML: "<p>x</p>", Title: "Chart", Height: 420, BaseURL: srv.URL})) {
				t.Fatalf("renderId=%q input=%+v", body.RenderID, svc.input)
			}
		})
	}
}

// A page with its local images inlined runs to 25 MiB, and its JSON body must
// reach the service whole on both routes.
func TestRenderRoutesAcceptA25MiBPage(t *testing.T) {
	// Markup is the worst case for the body: encoding/json writes each < as
	// \u003c, and the CLI accepts at most 1 MiB of it.
	page := strings.Repeat("<", 1<<20) + strings.Repeat("A", 24<<20)
	svc := &renderCheckStub{renderStub: &renderStub{fakeConversationService: &fakeConversationService{}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{DataDir: t.TempDir()}, log, nil, httpd.APIDeps{
		Sessions: newFakeSessionService(), Conversations: svc,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for path, status := range map[string]int{"/renders": http.StatusCreated, "/renders/check": http.StatusOK} {
		body, err := json.Marshal(map[string]any{"html": page, "title": "x"})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(srv.URL+"/api/v1/sessions/proj-1"+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("%s with a %d-byte body = %d, want %d", path, len(body), resp.StatusCode, status)
		}
	}
	if svc.input.HTML != page || svc.checkInput.HTML != page {
		t.Fatalf("service got %d and %d bytes, want %d", len(svc.input.HTML), len(svc.checkInput.HTML), len(page))
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
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"ok", nil, http.StatusOK, "", ""},
		{"no desktop app", chatsvc.ErrRenderCheckUnavailable, http.StatusServiceUnavailable, "RENDER_CHECK_UNAVAILABLE", ""},
		{"desktop could not check the page", browserruntime.CommandError{Code: "BROWSER_COMMAND_FAILED", Message: "render check page did not load within 15000 ms"},
			http.StatusUnprocessableEntity, "RENDER_CHECK_FAILED", ""},
		{"tui session", chatsvc.ErrNotChatMode, http.StatusConflict, "SESSION_MODE_MISMATCH", renderNeedsChatMessage},
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
				Code       string         `json:"code"`
				Message    string         `json:"message"`
				Details    map[string]any `json:"details"`
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
			if tc.message != "" && body.Message != tc.message {
				t.Fatalf("message=%q, want %q", body.Message, tc.message)
			}
			if code := (browserruntime.CommandError{}); errors.As(tc.err, &code) {
				if body.Message != code.Message || body.Details["desktopCode"] != code.Code {
					t.Fatalf("message=%q details=%v, want the desktop's own reason", body.Message, body.Details)
				}
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
