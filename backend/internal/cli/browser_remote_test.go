package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// executeCLIRoot runs args through executeWithDeps so tests cover the
// root-level remote-browser routing, not just the assembled command tree.
func executeCLIRoot(t *testing.T, deps Deps, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	deps.Out = &out
	deps.Err = &errOut
	if deps.Sleep == nil {
		deps.Sleep = func(time.Duration) {}
	}
	err := executeWithDeps(deps, args)
	return out.String(), errOut.String(), err
}

type remoteBrowserCapture struct {
	auth        string
	contentType string
	body        remoteBrowserRequest
}

func remoteBrowserServer(t *testing.T, capture *remoteBrowserCapture, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		capture.auth = r.Header.Get("Authorization")
		capture.contentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&capture.body)
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrowserRemoteForwardedToConfiguredService(t *testing.T) {
	capture := &remoteBrowserCapture{}
	srv := remoteBrowserServer(t, capture, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"https://example.com/\n"}`))
	})
	t.Setenv(envAgentBrowserURL, srv.URL)
	t.Setenv(envAgentBrowserToken, "remote-token")

	out, errOut, err := executeCLIRoot(t, Deps{HTTPClient: srv.Client()}, "browser", "get", "url")
	if err != nil || errOut != "" {
		t.Fatalf("get url err=%v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "https://example.com/") {
		t.Fatalf("output = %q", out)
	}
	if capture.auth != "Bearer remote-token" || capture.contentType != "application/json" {
		t.Fatalf("request auth=%q content-type=%q", capture.auth, capture.contentType)
	}
	if len(capture.body.Args) != 2 || capture.body.Args[0] != "get" || capture.body.Args[1] != "url" {
		t.Fatalf("forwarded args = %#v", capture.body.Args)
	}
}

func TestBrowserRemoteWritesReturnedFilesAndReportsServiceError(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "shots", "page.png")
	capture := &remoteBrowserCapture{}
	srv := remoteBrowserServer(t, capture, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteBrowserResponse{
			Output: "saved\n",
			Error:  "boom",
			Files:  map[string]string{saved: "aGVsbG8="},
		})
	})
	t.Setenv(envAgentBrowserURL, srv.URL)
	t.Setenv(envAgentBrowserToken, "remote-token")

	_, errOut, err := executeCLIRoot(t, Deps{HTTPClient: srv.Client()}, "browser", "screenshot", saved)
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(errOut, "boom") {
		t.Fatalf("screenshot err=%v stderr=%s", err, errOut)
	}
	data, readErr := os.ReadFile(saved)
	if readErr != nil || string(data) != "hello" {
		t.Fatalf("returned file = %q err=%v", data, readErr)
	}
	if len(capture.body.Args) != 2 || capture.body.Args[0] != "screenshot" {
		t.Fatalf("forwarded args = %#v", capture.body.Args)
	}
}

func TestBrowserRemoteClearErrorsForMissingPrerequisites(t *testing.T) {
	t.Setenv(envAgentBrowserURL, "http://127.0.0.1:9")
	t.Setenv(envAgentBrowserToken, "")

	_, _, err := executeCLIRoot(t, Deps{}, "browser", "get", "url")
	if err == nil || !strings.Contains(err.Error(), envAgentBrowserToken) || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("missing token err=%v", err)
	}

	t.Setenv(envAgentBrowserToken, "remote-token")
	deps := Deps{HTTPClient: &http.Client{Timeout: 500 * time.Millisecond}}
	_, _, err = executeCLIRoot(t, deps, "browser", "get", "url")
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("unreachable service err=%v", err)
	}
}

func TestBrowserRemoteRoutingRequiresBrowserCommand(t *testing.T) {
	t.Setenv(envAgentBrowserURL, "http://127.0.0.1:9")
	t.Setenv(envAgentBrowserToken, "remote-token")

	cfg, rest, err := remoteBrowserForward(os.Getenv, []string{"status"})
	if cfg != nil || rest != nil || err != nil {
		t.Fatalf("status must not forward: cfg=%v rest=%v err=%v", cfg, rest, err)
	}
	cfg, rest, err = remoteBrowserForward(os.Getenv, []string{"browser", "snapshot", "--interactive"})
	if cfg == nil || err != nil {
		t.Fatalf("browser must forward: cfg=%v err=%v", cfg, err)
	}
	if strings.Join(rest, " ") != "snapshot --interactive" {
		t.Fatalf("rest = %#v", rest)
	}
	cfg, rest, err = remoteBrowserForward(os.Getenv, nil)
	if cfg != nil || rest != nil || err != nil {
		t.Fatalf("no args must not forward: cfg=%v rest=%v err=%v", cfg, rest, err)
	}
}
