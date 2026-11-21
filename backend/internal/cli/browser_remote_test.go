package cli

import (
	"bytes"
	"encoding/base64"
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

// TestBrowserRemoteSlowResponseSucceedsWithProductionClient covers the review
// finding that the default CLI HTTP client's 2-second timeout, not the command
// context, bounded remote browser requests: a healthy response slower than
// the probe timeout must still succeed with production defaults.
func TestBrowserRemoteSlowResponseSucceedsWithProductionClient(t *testing.T) {
	capture := &remoteBrowserCapture{}
	srv := remoteBrowserServer(t, capture, func(w http.ResponseWriter) {
		time.Sleep(2500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"https://example.com/\n"}`))
	})
	t.Setenv(envAgentBrowserURL, srv.URL)
	t.Setenv(envAgentBrowserToken, "remote-token")

	deps := Deps{HTTPClient: &http.Client{Timeout: 2 * time.Second}}
	out, errOut, err := executeCLIRoot(t, deps, "browser", "get", "url")
	if err != nil || errOut != "" {
		t.Fatalf("slow response with 2s client err=%v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "https://example.com/") {
		t.Fatalf("output = %q", out)
	}
}

// TestBrowserRemoteAcceptsBase64PayloadsPast64MiB covers the review finding
// that a valid large file (base64 of up to 64 MiB of content) plus JSON
// overhead exceeded the old 64 MiB whole-response cap: decoding must succeed
// past that boundary and the cap must exceed a maximum file's encoded size.
func TestBrowserRemoteAcceptsBase64PayloadsPast64MiB(t *testing.T) {
	if remoteBrowserMaxResponseBytes < (64<<20)*4/3 {
		t.Fatalf("response cap %d cannot carry one maximum 64 MiB file base64-encoded", remoteBrowserMaxResponseBytes)
	}
	// ~50 MiB of content encodes to ~66.7 MiB of base64: past the old cap,
	// comfortably inside the new one.
	content := bytes.Repeat([]byte("x"), 50<<20)
	encoded := base64.StdEncoding.EncodeToString(content)
	dir := t.TempDir()
	saved := filepath.Join(dir, "shot.png")
	body := remoteBrowserResponse{
		Output: "saved\n",
		// The service keys returned files by the path the command named.
		Files: map[string]string{saved: encoded},
	}
	encodedBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encodedBody)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envAgentBrowserURL, srv.URL)
	t.Setenv(envAgentBrowserToken, "remote-token")

	out, _, err := executeCLIRoot(t, Deps{HTTPClient: srv.Client()}, "browser", "screenshot", saved)
	if err != nil || !strings.Contains(out, "saved") {
		t.Fatalf("large payload err=%v out=%q", err, out)
	}
	info, statErr := os.Stat(saved)
	if statErr != nil || info.Size() != int64(len(content)) {
		t.Fatalf("saved file size=%d err=%v, want %d", info.Size(), statErr, len(content))
	}
}

// TestBrowserRemoteGoesThroughTheDaemonGate covers the session environment a
// self-hosted host now gives agents: the service URL without its token. Every
// command goes to the agent's own daemon, which runs the approval gate.
func TestBrowserRemoteGoesThroughTheDaemonGate(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	dir := t.TempDir()
	saved := filepath.Join(dir, "page.png")
	var got struct {
		path, capability string
		body             map[string]any
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.capability = r.Header.Get(browserCapabilityHeader)
		got.body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "application/json")
		args, _ := got.body["args"].([]any)
		if len(args) > 0 && args[0] == "click" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden","code":"BROWSER_ACTION_DENIED","message":"The user denied this action: Click \"Pay\". It was not performed.","requestId":"r1"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"output": "saved", "files": map[string]string{saved: "aGVsbG8="}})
	}))
	t.Cleanup(daemon.Close)
	writeRunFileFor(t, cfg, daemon)
	t.Setenv(envAgentBrowserURL, "http://127.0.0.1:9") // the service itself is never contacted
	t.Setenv(envAgentBrowserToken, "")
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, _, err := executeCLIRoot(t, deps, "browser", "screenshot", saved)
	if err != nil || !strings.Contains(out, "saved") {
		t.Fatalf("screenshot out=%q err=%v", out, err)
	}
	if got.path != "/api/v1/browser/remote-commands" || got.capability != "capability-1" || got.body["sessionId"] != "ao-1" {
		t.Fatalf("daemon request = %+v", got)
	}
	if data, _ := os.ReadFile(saved); string(data) != "hello" {
		t.Fatalf("returned file = %q", data)
	}

	_, _, err = executeCLIRoot(t, deps, "browser", "click", "@e2")
	if err == nil || !strings.Contains(err.Error(), "BROWSER_ACTION_DENIED") {
		t.Fatalf("click err = %v", err)
	}
}

func TestBrowserRemoteDaemonModeFailsClosed(t *testing.T) {
	setBrowserIdentity(t)
	setConfigEnv(t) // no run file: the daemon is not running
	service := remoteBrowserServer(t, &remoteBrowserCapture{}, func(w http.ResponseWriter) {
		t.Error("the CLI reached the browser service directly")
	})
	t.Setenv(envAgentBrowserURL, service.URL)
	t.Setenv(envAgentBrowserToken, "")
	_, _, err := executeCLIRoot(t, Deps{ProcessAlive: func(int) bool { return true }}, "browser", "click", "@e2")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v", err)
	}
}
