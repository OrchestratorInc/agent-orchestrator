package browserchrome

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
)

// TestChromeE2E drives the real AO Browser Bridge extension in a real Chrome.
// It is opt-in; run it by hand after `ao browser chrome setup`:
//
//	AO_CHROME_E2E_ENDPOINT=<data>/browser-chrome/endpoint.json \
//	AO_CHROME_E2E_HOST=<path to the ao binary the host wrapper runs> \
//	AO_CHROME_E2E_CDP=http://127.0.0.1:9333 \
//	go test ./internal/browserchrome -run TestChromeE2E -v
//
// Chrome must run with the extension loaded and --remote-debugging-port, used
// here only to click the consent prompt the way a person would. Stop the
// daemon first: this test serves the Chrome runtime socket itself.
func TestChromeE2E(t *testing.T) {
	endpointPath := os.Getenv("AO_CHROME_E2E_ENDPOINT")
	hostExe := os.Getenv("AO_CHROME_E2E_HOST")
	cdp := os.Getenv("AO_CHROME_E2E_CDP")
	if endpointPath == "" || hostExe == "" || cdp == "" {
		t.Skip("set AO_CHROME_E2E_ENDPOINT, AO_CHROME_E2E_HOST and AO_CHROME_E2E_CDP to run against real Chrome")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	token, err := browserruntime.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	broker := browserruntime.New(nil, token)
	broker.SetTransport(Transport)
	broker.SetExclusive(true)
	broker.SetPeerVerifier(PeerVerifier(hostExe))
	dir, err := os.MkdirTemp("/tmp", "ao-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ln, err := net.Listen("unix", dir+"/chrome.sock")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()
	if err := WriteEndpoint(endpointPath, Endpoint{Address: ln.Addr().String(), Token: token, PID: os.Getpid(), Protocol: browserruntime.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	defer RemoveEndpoint(endpointPath, os.Getpid())

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = fmt.Fprint(w, `<!doctype html><title>AO test shop</title>
<h1>AO test shop</h1>
<label for="email">Email</label><input id="email">
<button id="save" onclick="document.getElementById('status').textContent='Saved '+document.getElementById('email').value">Save</button>
<button id="ask" onclick="window.confirmed = confirm('Really?')">Ask</button>
<p id="status">Idle</p><p id="keys">No keys</p>
<a href="/next">Next page</a>
<iframe src="/frame" title="Embedded form" width="300" height="80"></iframe>
<script>console.log('shop loaded'); document.addEventListener('keydown', e => { if (e.key === 'Enter') document.getElementById('keys').textContent = 'Enter pressed' })</script>`)
		case "/next":
			_, _ = fmt.Fprint(w, `<!doctype html><title>Next</title><h1>Second page</h1>`)
		case "/frame":
			_, _ = fmt.Fprint(w, `<!doctype html><button>Inside frame</button>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	deadline := time.Now().Add(90 * time.Second)
	for !broker.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("the extension did not connect; is Chrome running with it loaded?")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("connected: %+v", broker.Status())

	const session = "e2e-session"
	run := func(action string, args map[string]interface{}) map[string]interface{} {
		t.Helper()
		result, err := broker.Execute(ctx, session, action, args)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		value, _ := result.Value.(map[string]interface{})
		return value
	}
	expectError := func(action string, args map[string]interface{}, code string) {
		t.Helper()
		_, err := broker.Execute(ctx, session, action, args)
		var commandErr browserruntime.CommandError
		if err == nil || !errorAs(err, &commandErr) || commandErr.Code != code {
			t.Fatalf("%s error = %v, want %s", action, err, code)
		}
	}

	// The first command waits for consent; answer it like a person would.
	consentDone := make(chan error, 1)
	go func() { consentDone <- clickConsent(ctx, cdp) }()
	opened := run("open", map[string]interface{}{"url": site.URL + "/"})
	if err := <-consentDone; err != nil {
		t.Fatalf("consent: %v", err)
	}
	if opened["title"] != "AO test shop" {
		t.Fatalf("open = %#v", opened)
	}

	snapshot := run("snapshot", map[string]interface{}{"interactive": true})
	text, _ := snapshot["text"].(string)
	t.Logf("snapshot:\n%s", text)
	for _, want := range []string{`textbox "Email"`, `button "Save"`, `link "Next page"`, `iframe "Embedded form"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot missing %s", want)
		}
	}

	filled := run("act", map[string]interface{}{"instruction": "email", "action": "fill", "value": "me@example.com"})
	if filled["outcome"] != "matched" {
		t.Fatalf("fill act = %#v", filled)
	}
	saved := run("act", map[string]interface{}{
		"instruction":   "the save button",
		"postcondition": map[string]interface{}{"kind": "text", "value": "Saved me@example.com"},
	})
	postcondition, _ := saved["postcondition"].(map[string]interface{})
	if saved["outcome"] != "matched" || postcondition["status"] != "satisfied" {
		t.Fatalf("save act = %#v", saved)
	}
	if result, _ := saved["result"].(map[string]interface{}); result["inputTier"] != "trusted" {
		t.Fatalf("save used %v input, want trusted", result["inputTier"])
	}

	run("press", map[string]interface{}{"key": "Enter"})
	run("wait", map[string]interface{}{"text": "Enter pressed", "timeoutMs": 5000})

	delta := run("snapshot", map[string]interface{}{"interactive": true, "delta": true})
	if delta["kind"] != "full" {
		t.Fatalf("first delta = %#v", delta)
	}
	if again := run("snapshot", map[string]interface{}{"interactive": true, "delta": true}); again["kind"] != "unchanged" {
		t.Fatalf("second delta = %#v", again)
	}

	var frameRef string
	if refs, ok := snapshot["refs"].(map[string]interface{}); ok {
		for ref, raw := range refs {
			if info, _ := raw.(map[string]interface{}); info["role"] == "iframe" {
				frameRef = ref
			}
		}
	}
	run("frame", map[string]interface{}{"target": frameRef})
	inside := run("snapshot", map[string]interface{}{"interactive": true})
	if !strings.Contains(fmt.Sprint(inside["text"]), `button "Inside frame"`) {
		t.Fatalf("frame snapshot = %#v", inside)
	}
	frameClick := run("act", map[string]interface{}{"instruction": "inside frame button"})
	if frameClick["outcome"] != "matched" {
		t.Fatalf("frame act = %#v", frameClick)
	}
	run("frame", map[string]interface{}{"target": "main"})

	shot := run("screenshot", map[string]interface{}{"annotate": true})
	png, err := base64.StdEncoding.DecodeString(fmt.Sprint(shot["data"]))
	if err != nil || len(png) < 100 || string(png[1:4]) != "PNG" {
		t.Fatalf("screenshot: %v (%d bytes)", err, len(png))
	}
	if annotations, _ := shot["annotations"].([]interface{}); len(annotations) == 0 {
		t.Fatal("annotated screenshot has no annotations")
	}

	run("network-start", map[string]interface{}{"durationSeconds": 30})
	run("open", map[string]interface{}{"url": site.URL + "/next"})
	network := run("network-list", nil)
	if requests, _ := network["requests"].([]interface{}); len(requests) == 0 {
		t.Fatalf("network = %#v", network)
	}
	run("network-stop", nil)

	run("open", map[string]interface{}{"url": site.URL + "/"})
	console := run("console", nil)
	if !strings.Contains(fmt.Sprint(console["messages"]), "shop loaded") {
		t.Fatalf("console = %#v", console)
	}

	asked := run("act", map[string]interface{}{"instruction": "ask button", "postcondition": map[string]interface{}{"kind": "dialog"}})
	if pc, _ := asked["postcondition"].(map[string]interface{}); pc["status"] != "satisfied" {
		t.Fatalf("dialog act = %#v", asked)
	}
	if status := run("dialog", map[string]interface{}{"operation": "status"}); status["open"] != true || status["type"] != "confirm" {
		t.Fatalf("dialog status = %#v", status)
	}
	run("dialog", map[string]interface{}{"operation": "accept"})

	created := run("tab-new", map[string]interface{}{"url": site.URL + "/next"})
	if created["id"] != "t2" {
		t.Fatalf("tab-new = %#v", created)
	}
	if tabs, _ := run("tabs", nil)["tabs"].([]interface{}); len(tabs) != 2 {
		t.Fatalf("tabs = %#v", tabs)
	}
	run("tab-close", map[string]interface{}{"tabId": "t2"})

	expectError("open", map[string]interface{}{"url": "https://www.paypal.com/"}, "BROWSER_SITE_BLOCKED")
	expectError("open", map[string]interface{}{"url": "file:///etc/hosts"}, "BROWSER_URL_FORBIDDEN")
	expectError("devtools-open", nil, "BROWSER_ACTION_UNSUPPORTED_IN_TARGET")

	if err := broker.DestroySession(ctx, session); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

func errorAs(err error, target *browserruntime.CommandError) bool {
	return errors.As(err, target)
}

// clickConsent waits for the extension's consent window and clicks Allow
// through the Chrome DevTools Protocol, standing in for the person.
func clickConsent(ctx context.Context, cdp string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(cdp + "/json/list")
		if err == nil {
			var targets []struct {
				URL                  string `json:"url"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&targets)
			_ = resp.Body.Close()
			for _, target := range targets {
				if strings.Contains(target.URL, "/consent.html?session=") && target.WebSocketDebuggerURL != "" {
					conn, _, err := websocket.Dial(ctx, target.WebSocketDebuggerURL, nil)
					if err != nil {
						return err
					}
					payload, _ := json.Marshal(map[string]interface{}{
						"id":     1,
						"method": "Runtime.evaluate",
						"params": map[string]interface{}{"expression": "document.getElementById('allow').click()", "userGesture": true},
					})
					err = conn.Write(ctx, websocket.MessageText, payload)
					_, _, _ = conn.Read(ctx)
					_ = conn.Close(websocket.StatusNormalClosure, "")
					return err
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("no consent window appeared")
}
