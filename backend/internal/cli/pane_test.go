package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type paneCapture struct {
	method string
	path   string
	body   string
}

func paneServer(t *testing.T, status int, respBody string) (*httptest.Server, *paneCapture) {
	t.Helper()
	capture := &paneCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capture.method, capture.path, capture.body = r.Method, r.URL.Path, string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

const paneLayoutBody = `{"sessionId":"aa-47","layout":{"enabled":true,"selected":"sh-1","shown":"g1",
"tabs":[{"key":"session","kind":"session","title":"Fix login"},{"key":"sh-1","kind":"terminal","title":"Terminal 1","agent":"claude-code"}],
"groups":[{"id":"g1","focus":"sh-1","panes":[{"key":"session","x":0,"y":0,"width":0.5,"height":1},{"key":"sh-1","x":0.5,"y":0,"width":0.5,"height":1}]}]}}`

func TestPaneListsTabsAndSplits(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := paneServer(t, http.StatusOK, paneLayoutBody)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/sessions/aa-47/panes" {
		t.Fatalf("hit %s %s", capture.method, capture.path)
	}
	for _, want := range []string{"Split panes: on", "On screen: sh-1", "Terminal 1 (claude-code)", "g1 (on screen)", "50% wide", "focused"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPaneSplitSendsTheRequest(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := paneServer(t, http.StatusOK, paneLayoutBody)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "split", "terminal", "--side", "bottom", "--beside", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/aa-47/panes" {
		t.Fatalf("hit %s %s", capture.method, capture.path)
	}
	var req map[string]string
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req["action"] != "split" || req["tab"] != "terminal" || req["side"] != "bottom" || req["beside"] != "session" {
		t.Fatalf("body = %s", capture.body)
	}
}

func TestPaneRejectsABadSideWithoutCallingTheDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := paneServer(t, http.StatusOK, paneLayoutBody)
	writeRunFileFor(t, cfg, srv)

	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "split", "terminal", "--side", "middle"); err == nil {
		t.Fatal("expected an error for --side middle")
	}
	if capture.method != "" {
		t.Fatalf("daemon was called: %s %s", capture.method, capture.path)
	}
}

func TestPaneShowsTheWindowsRefusal(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, _ := paneServer(t, http.StatusConflict, `{"code":"SPLIT_PANES_OFF","message":"Split panes is off in the AO window."}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "ungroup")
	if err == nil {
		t.Fatal("expected the refusal as an error")
	}
	if !strings.Contains(err.Error()+errOut, "Split panes is off") {
		t.Fatalf("err = %v, stderr = %s", err, errOut)
	}
}

func TestPaneNeedsASession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	setConfigEnv(t)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane"); err == nil || !strings.Contains(err.Error(), "needs a session") {
		t.Fatalf("err = %v", err)
	}
}
