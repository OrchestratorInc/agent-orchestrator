package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

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

func servePanes(t *testing.T, svc controllers.PaneService, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.PanesController{Svc: svc}).Register(r)
	req := httptest.NewRequest(method, "/sessions/s-1/panes", strings.NewReader(body))
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
