package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserTargetCommand(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/browser/commands" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		capture.path = string(body)
		target := "panel"
		if strings.Contains(string(body), `"chrome"`) {
			target = "chrome"
		}
		_, _ = io.WriteString(w, `{"requestId":"r1","sessionId":"ao-1","action":"target","result":{"target":"`+target+`","connected":false,"transport":"chrome-extension"}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "browser", "target", "chrome")
	if err != nil {
		t.Fatalf("target chrome: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(capture.path, `"action":"target"`) || !strings.Contains(capture.path, `"target":"chrome"`) {
		t.Fatalf("request = %s", capture.path)
	}
	if !strings.Contains(out, "your Chrome (AO Browser Bridge), not connected") || !strings.Contains(out, "ao browser chrome setup") {
		t.Fatalf("output = %q", out)
	}

	out, _, err = executeCLI(t, deps, "browser", "target")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capture.path, `"target":`) && strings.Contains(capture.path, `"args"`) {
		t.Fatalf("read request sent a target: %s", capture.path)
	}
	if !strings.Contains(out, "AO Browser panel") {
		t.Fatalf("output = %q", out)
	}
}

func TestBrowserChromeSetupAndStatus(t *testing.T) {
	cfg := setConfigEnv(t)
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/browser/chrome" {
			http.NotFound(w, r)
			return
		}
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		enabled := r.Method != http.MethodDelete
		state := `"enabled":false`
		if enabled {
			state = `"enabled":true`
		}
		_, _ = io.WriteString(w, `{`+state+`,"connected":false,"extensionId":"eefgahhblbmjpccfcnpkhkadgcccnibg","extensionPath":"/home/u/.ao/data/browser-chrome/extension","extensionBundled":true,"hostPath":"/h","channels":[{"name":"Google Chrome","available":true,"registered":`+map[bool]string{true: "true", false: "false"}[enabled]+`,"manifestPath":"/m"},{"name":"Google Chrome Beta","available":false,"registered":false,"manifestPath":"/b"}]}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "browser", "chrome", "setup")
	if err != nil {
		t.Fatalf("setup: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"Chrome mode: set up", "Google Chrome: registered", "Google Chrome Beta: not installed", "Load unpacked", "/home/u/.ao/data/browser-chrome/extension", "eefgahhblbmjpccfcnpkhkadgcccnibg"} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q:\n%s", want, out)
		}
	}
	out, _, err = executeCLI(t, deps, "browser", "chrome", "remove")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Chrome mode removed") {
		t.Fatalf("remove output = %q", out)
	}
	if _, _, err := executeCLI(t, deps, "browser", "chrome", "status"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,DELETE,GET" {
		t.Fatalf("methods = %v", methods)
	}
}
