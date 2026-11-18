package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type paneCapture struct {
	method     string
	path       string
	body       string
	capability string
}

func paneServer(t *testing.T, status int, respBody string) (*httptest.Server, *paneCapture) {
	t.Helper()
	capture := &paneCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The CLI also reports its own telemetry; that is not a daemon API call.
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		capture.method, capture.path, capture.body = r.Method, r.URL.Path, string(body)
		capture.capability = r.Header.Get("X-AO-Browser-Capability")
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
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
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
	if capture.capability != "cap-aa-47" {
		t.Fatalf("capability header = %q, want the session's capability", capture.capability)
	}
	for _, want := range []string{"Split panes: on", "On screen: sh-1", "Terminal 1 (claude-code)", "g1 (on screen)", "50% wide", "focused", "ao pane run sh-1 <command>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPaneSplitSendsTheRequest(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
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
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
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
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
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
	t.Setenv("AO_BROWSER_CAPABILITY", "")
	setConfigEnv(t)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane"); err == nil || !strings.Contains(err.Error(), "must run inside an AO session") {
		t.Fatalf("err = %v", err)
	}
}

// paneTerminalServer fakes a terminal whose screen shows the typed command's
// output once it has been typed.
func paneTerminalServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	screen := "$ "
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/sessions/aa-47/panes/input":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if text, ok := req["text"].(string); ok {
				screen = "$ " + text + "\nREADME.md\n$ "
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/sessions/aa-47/panes/output":
			_ = json.NewEncoder(w).Encode(map[string]string{"sessionId": "aa-47", "tab": r.URL.Query().Get("tab"), "output": screen})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestPaneRunTypesTheCommandAndPrintsTheSettledScreen(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
	cfg := setConfigEnv(t)
	srv, calls := paneTerminalServer(t)
	writeRunFileFor(t, cfg, srv)
	previous := paneSettleInterval
	paneSettleInterval = 20 * time.Millisecond
	t.Cleanup(func() { paneSettleInterval = previous })

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "run", "sh-1", "ls", "-la")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "$ ls -la\nREADME.md") {
		t.Fatalf("output = %q", out)
	}
	if first := (*calls)[0]; !strings.Contains(first, "/panes/input") || !strings.Contains(first, `"text":"ls -la"`) || !strings.Contains(first, `"enter":true`) {
		t.Fatalf("first call = %s", first)
	}
}

func TestPaneReadAndInterrupt(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
	cfg := setConfigEnv(t)
	srv, calls := paneTerminalServer(t)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "read", "sh-1", "--lines", "20")
	if err != nil || strings.TrimSpace(out) != "$" {
		t.Fatalf("read: out = %q, err = %v", out, err)
	}
	if !strings.Contains((*calls)[0], "lines=20") || !strings.Contains((*calls)[0], "tab=sh-1") {
		t.Fatalf("read call = %s", (*calls)[0])
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "interrupt", "sh-1"); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if last := (*calls)[len(*calls)-1]; !strings.Contains(last, `"interrupt":true`) {
		t.Fatalf("interrupt call = %s", last)
	}
}

func TestPaneSplitRunRefusesANonTerminalTab(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
	setConfigEnv(t)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "split", "file:README.md", "--run", "ls"); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("err = %v", err)
	}
}

func TestPaneCloseGoesThroughTheWindowAndFallsBackToTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name       string
		windowCode int
		windowBody string
		wantInput  bool
	}{
		{"window shows the session", http.StatusOK, paneLayoutBody, false},
		{"no window shows it", http.StatusConflict, `{"code":"SESSION_NOT_ON_SCREEN","message":"not on screen"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "aa-47")
			t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
			t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
			cfg := setConfigEnv(t)
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/internal/") {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, _ := io.ReadAll(r.Body)
				calls = append(calls, r.URL.Path+" "+string(body))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/sessions/aa-47/panes/input" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(tc.windowCode)
				_, _ = io.WriteString(w, tc.windowBody)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)

			if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "close", "sh-1"); err != nil {
				t.Fatalf("close: %v\nstderr=%s", err, errOut)
			}
			if !strings.Contains(calls[0], `"action":"close"`) {
				t.Fatalf("first call = %s, want the window close", calls[0])
			}
			usedDaemon := len(calls) == 2 && strings.Contains(calls[1], `"close":true`)
			if usedDaemon != tc.wantInput {
				t.Fatalf("calls = %v, want daemon fallback = %v", calls, tc.wantInput)
			}
		})
	}
}

func TestPaneNeedsTheSessionCapability(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "")
	setConfigEnv(t)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "read", "sh-1"); err == nil || !strings.Contains(err.Error(), "AO_BROWSER_CAPABILITY") {
		t.Fatalf("err = %v", err)
	}
}

func TestPaneRunReturnsAReadErrorThatIsNotATimeout(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-aa-47")
	cfg := setConfigEnv(t)
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch r.URL.Path {
		case "/api/v1/sessions/aa-47/panes/input":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/sessions/aa-47/panes/output":
			reads++
			if reads > 1 {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sessionId": "aa-47", "tab": "sh-1", "output": "$ "})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	previous := paneSettleInterval
	paneSettleInterval = 20 * time.Millisecond
	t.Cleanup(func() { paneSettleInterval = previous })

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "pane", "run", "sh-1", "ls")
	if err == nil {
		t.Fatalf("a failing read passed as success; output = %q", out)
	}
	if strings.Contains(out, "timed out") {
		t.Fatalf("a read error was reported as a timeout: %q", out)
	}
}
