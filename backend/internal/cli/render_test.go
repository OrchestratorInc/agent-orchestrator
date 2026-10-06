package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderServer(t *testing.T, status int, respBody string) (*httptest.Server, *previewCapture) {
	t.Helper()
	capture := &previewCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The root command pings /internal/telemetry/cli-invoked before every
		// run; only the render route counts as the CLI calling the daemon.
		if !strings.HasSuffix(r.URL.Path, "/renders") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capture.called, capture.body, capture.path, capture.method = true, string(body), r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func writePage(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chart.html")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderPostsThePageToTheSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated,
		`{"renderId":"r1","activityId":"a1","path":"/api/v1/sessions/aa-47/renders/r1"}`)
	writeRunFileFor(t, cfg, srv)
	page := writePage(t, []byte("<!doctype html><p>chart</p>"))

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", page, "--title", "Turns by day", "--height", "420")
	if err != nil {
		t.Fatalf("render: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/aa-47/renders" {
		t.Fatalf("hit %s %s", capture.method, capture.path)
	}
	var req struct {
		HTML   string `json:"html"`
		Title  string `json:"title"`
		Height int    `json:"height"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.HTML != "<!doctype html><p>chart</p>" || req.Title != "Turns by day" || req.Height != 420 {
		t.Fatalf("request = %+v", req)
	}
	if !strings.Contains(out, "r1") {
		t.Fatalf("stdout = %q, want the render id", out)
	}
}

func TestRenderOutsideASessionDoesNotCallTheDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", writePage(t, []byte("<p>x</p>")), "--title", "x")
	if err == nil || capture.called {
		t.Fatalf("err=%v called=%v; want a usage error and no request", err, capture.called)
	}
}

func TestRenderRefusesOversizedFilesBeforeSending(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", writePage(t, make([]byte, 1<<20+1)), "--title", "x")
	if err == nil || capture.called {
		t.Fatalf("err=%v called=%v; want a size error and no request", err, capture.called)
	}
}
