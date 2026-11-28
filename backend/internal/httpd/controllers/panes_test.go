package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/panes"
)

type fakePanes struct {
	layout    json.RawMessage
	connected bool
	result    panes.Result
	err       error
	action    string
	args      string
}

func (f *fakePanes) Layout(string) (json.RawMessage, bool) { return f.layout, f.layout != nil }
func (f *fakePanes) Connected() bool                       { return f.connected }
func (f *fakePanes) Execute(_ context.Context, _ string, action string, args json.RawMessage) (panes.Result, error) {
	f.action, f.args = action, string(args)
	return f.result, f.err
}

// paneAuth stands in for the session lookup and capability check: session
// "s-1" owns capability "cap-s-1".
type paneAuth struct{}

func (paneAuth) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	if id != "s-1" {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "no such session")
	}
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}}, nil
}

func (paneAuth) Valid(id domain.SessionID, token, _ string) bool {
	return id == "s-1" && token == "cap-s-1"
}

func paneRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("X-AO-Browser-Capability", "cap-s-1")
	return req
}

func servePanes(t *testing.T, svc controllers.PaneService, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.PanesController{Svc: svc, Sessions: paneAuth{}, Capabilities: paneAuth{}}).Register(r)
	req := paneRequest(method, "/sessions/s-1/panes", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestPanesLayoutReturnsTheWindowsReport(t *testing.T) {
	svc := &fakePanes{connected: true, layout: json.RawMessage(`{"enabled":true,"selected":"session","tabs":[{"key":"session","kind":"session","title":"Fix bug"}]}`)}
	rec := servePanes(t, svc, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var out controllers.PaneLayoutResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.SessionID != "s-1" || !out.Layout.Enabled || len(out.Layout.Tabs) != 1 || out.Layout.Groups == nil {
		t.Fatalf("layout = %+v", out)
	}
}

func TestPanesLayoutExplainsWhyThereIsNone(t *testing.T) {
	for _, tc := range []struct {
		connected bool
		status    int
		code      string
	}{
		{false, http.StatusServiceUnavailable, "NO_DESKTOP_WINDOW"},
		{true, http.StatusConflict, panes.CodeNotShown},
	} {
		rec := servePanes(t, &fakePanes{connected: tc.connected}, http.MethodGet, "")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
			t.Fatalf("connected=%v: status = %d, body = %s", tc.connected, rec.Code, rec.Body)
		}
	}
}

func TestPanesCommandRelaysAndReturnsTheNewLayout(t *testing.T) {
	svc := &fakePanes{connected: true, result: panes.Result{OK: true, Layout: json.RawMessage(`{"enabled":true,"selected":"sh-1"}`)}}
	rec := servePanes(t, svc, http.MethodPost, `{"action":"split","tab":"terminal","side":"bottom"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if svc.action != "split" || !strings.Contains(svc.args, `"side":"bottom"`) {
		t.Fatalf("relayed %s %s", svc.action, svc.args)
	}
	if !strings.Contains(rec.Body.String(), `"selected":"sh-1"`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestPanesCommandRejectsBadRequestsBeforeTheWindow(t *testing.T) {
	for _, body := range []string{`{"action":"split"}`, `{"action":"split","tab":"x","side":"middle"}`, `{"action":"explode"}`, `{"action":"remove"}`} {
		svc := &fakePanes{connected: true}
		rec := servePanes(t, svc, http.MethodPost, body)
		if rec.Code != http.StatusBadRequest || svc.action != "" {
			t.Fatalf("%s: status = %d, relayed = %q", body, rec.Code, svc.action)
		}
	}
}

func TestPanesCommandSurfacesTheWindowsRefusalAndTransportErrors(t *testing.T) {
	refused := servePanes(t, &fakePanes{result: panes.Result{Code: "SPLIT_PANES_OFF", Message: "Split panes is off."}}, http.MethodPost, `{"action":"ungroup"}`)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "SPLIT_PANES_OFF") {
		t.Fatalf("refused: %d %s", refused.Code, refused.Body)
	}
	noWindow := servePanes(t, &fakePanes{err: panes.ErrNoWindow}, http.MethodPost, `{"action":"ungroup"}`)
	if noWindow.Code != http.StatusServiceUnavailable || !strings.Contains(noWindow.Body.String(), "NO_DESKTOP_WINDOW") {
		t.Fatalf("no window: %d %s", noWindow.Code, noWindow.Body)
	}
	timeout := servePanes(t, &fakePanes{err: panes.ErrTimeout}, http.MethodPost, `{"action":"ungroup"}`)
	if timeout.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: %d %s", timeout.Code, timeout.Body)
	}
}

type fakePaneTerminals struct {
	typed       string
	enter       bool
	interrupted bool
	closed      bool
	output      string
	lines       int
	err         error
}

func (f *fakePaneTerminals) TypeIntoSessionTerminal(_ context.Context, _ domain.SessionID, _ string, text string, enter bool) error {
	f.typed, f.enter = text, enter
	return f.err
}

func (f *fakePaneTerminals) InterruptSessionTerminal(context.Context, domain.SessionID, string) error {
	f.interrupted = true
	return f.err
}

func (f *fakePaneTerminals) SessionTerminalOutput(_ context.Context, _ domain.SessionID, _ string, lines int) (string, error) {
	f.lines = lines
	return f.output, f.err
}

func (f *fakePaneTerminals) CloseSessionTerminal(context.Context, domain.SessionID, string) error {
	f.closed = true
	return f.err
}

func servePaneTerminals(t *testing.T, terms *fakePaneTerminals, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.PanesController{Svc: &fakePanes{}, Terminals: terms, Sessions: paneAuth{}, Capabilities: paneAuth{}}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, paneRequest(method, target, body))
	return rec
}

func TestPaneInputTypesACommandOrInterrupts(t *testing.T) {
	terms := &fakePaneTerminals{}
	if rec := servePaneTerminals(t, terms, http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1","text":"ls"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if terms.typed != "ls" || !terms.enter {
		t.Fatalf("typed %q enter=%v", terms.typed, terms.enter)
	}
	servePaneTerminals(t, terms, http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1","text":"y","enter":false}`)
	if terms.typed != "y" || terms.enter {
		t.Fatalf("typed %q enter=%v, want no Enter", terms.typed, terms.enter)
	}
	servePaneTerminals(t, terms, http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1","interrupt":true}`)
	if !terms.interrupted {
		t.Fatal("interrupt not sent")
	}
	servePaneTerminals(t, terms, http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1","close":true}`)
	if !terms.closed {
		t.Fatal("close not sent")
	}
	if rec := servePaneTerminals(t, terms, http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no text: status = %d", rec.Code)
	}
}

func TestPaneOutputReturnsTheScreenAndServiceErrors(t *testing.T) {
	terms := &fakePaneTerminals{output: "README.md"}
	rec := servePaneTerminals(t, terms, http.MethodGet, "/sessions/s-1/panes/output?tab=sh-1&lines=50", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"output":"README.md"`) || terms.lines != 50 {
		t.Fatalf("status = %d, body = %s, lines = %d", rec.Code, rec.Body, terms.lines)
	}
	if rec := servePaneTerminals(t, terms, http.MethodGet, "/sessions/s-1/panes/output?tab=sh-1&lines=zero", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad lines: status = %d", rec.Code)
	}
	terms.err = apierr.NotFound("PANE_TERMINAL_NOT_FOUND", "no such terminal")
	if rec := servePaneTerminals(t, terms, http.MethodGet, "/sessions/s-1/panes/output?tab=nope", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "PANE_TERMINAL_NOT_FOUND") {
		t.Fatalf("not found: status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestPaneRoutesRequireTheSessionCapability(t *testing.T) {
	terms := &fakePaneTerminals{}
	r := chi.NewRouter()
	(&controllers.PanesController{Svc: &fakePanes{connected: true}, Terminals: terms, Sessions: paneAuth{}, Capabilities: paneAuth{}}).Register(r)
	for _, tc := range []struct{ method, target, body, capability string }{
		{http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1","text":"rm -rf"}`, ""},
		{http.MethodPost, "/sessions/s-1/panes/input", `{"tab":"sh-1","text":"rm -rf"}`, "cap-other"},
		{http.MethodGet, "/sessions/s-1/panes/output?tab=sh-1", "", ""},
		{http.MethodGet, "/sessions/s-1/panes", "", "wrong"},
		{http.MethodPost, "/sessions/s-1/panes", `{"action":"ungroup"}`, ""},
	} {
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		if tc.capability != "" {
			req.Header.Set("X-AO-Browser-Capability", tc.capability)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "PANE_CAPABILITY_INVALID") {
			t.Fatalf("%s %s cap=%q: status = %d, body = %s", tc.method, tc.target, tc.capability, rec.Code, rec.Body)
		}
	}
	if terms.typed != "" {
		t.Fatalf("typed %q without the capability", terms.typed)
	}
}
