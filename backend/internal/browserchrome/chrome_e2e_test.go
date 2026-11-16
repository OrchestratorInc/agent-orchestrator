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
// Chrome must run with the extension loaded, --use-mock-keychain (or macOS asks
// for the login password on every launch and pages stall until it is answered)
// and --remote-debugging-port, used here only to click the consent prompt the
// way a person would, and to act as the user on tabs and windows. Stop the
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

	// The rest uses the extension's own APIs to do what a person would.
	extension := func(expression string) map[string]interface{} {
		t.Helper()
		value, err := evalInExtension(ctx, cdp, expression)
		if err != nil {
			t.Fatalf("extension %s: %v", expression, err)
		}
		return value
	}
	windowOf := func(group string) int {
		t.Helper()
		value := extension(fmt.Sprintf(`(async () => {
			const [g] = await chrome.tabGroups.query({ title: %q });
			const [tab] = await chrome.tabs.query({ groupId: g.id });
			return { windowId: tab.windowId, tabId: tab.id, others: (await chrome.tabs.query({ windowId: tab.windowId })).length };
		})()`, group))
		return int(value["windowId"].(float64))
	}

	// The agent works in a tab group in the user's window, behind the tab the
	// user has in front, without opening a window of its own.
	agentWindow := windowOf("Orchestrator · " + session)
	if windows := extension(fmt.Sprintf(`chrome.windows.getAll({ windowTypes: ["normal"] }).then((all) => ({ count: all.length, ours: all.some((w) => w.id === %d) }))`, agentWindow)); windows["count"].(float64) != 1 || windows["ours"] != true {
		t.Fatalf("normal windows = %v, want only the user's window", windows)
	}
	if front := extension(fmt.Sprintf(`chrome.tabs.query({ windowId: %d, active: true }).then(([tab]) => ({ grouped: tab.groupId !== -1 }))`, agentWindow)); front["grouped"] == true {
		t.Fatal("the agent's tab took the front of the user's window")
	}

	// The user switches to their own tab in the agent's window: it stays in
	// front, while input and screenshots still reach the agent's tab.
	run("open", map[string]interface{}{"url": site.URL + "/"})
	personal := int(extension(fmt.Sprintf(`chrome.tabs.create({ windowId: %d, active: true, url: %q }).then((tab) => ({ id: tab.id }))`, agentWindow, site.URL+"/next"))["id"].(float64))
	isFront := func(tabID int) bool {
		t.Helper()
		return extension(fmt.Sprintf(`chrome.tabs.get(%d).then((tab) => ({ active: tab.active }))`, tabID))["active"].(bool)
	}
	run("act", map[string]interface{}{"instruction": "save button"})
	run("wait", map[string]interface{}{"text": "Saved", "timeoutMs": 5000})
	if tier := inputTier(run("act", map[string]interface{}{"instruction": "save button"})); tier != "trusted" {
		t.Fatalf("input tier behind the user's tab = %v", tier)
	}
	if shot := run("screenshot", nil); shot["data"] == "" || shot["data"] == nil {
		t.Fatal("no screenshot of the agent's tab behind the user's tab")
	}
	if !isFront(personal) {
		t.Fatal("the agent switched away from the user's tab")
	}
	extension(fmt.Sprintf(`chrome.tabs.remove(%d).then(() => ({}))`, personal))

	// A minimized (hidden) agent window still takes trusted input and is
	// never restored. Chrome stops drawing minimized windows, so a screenshot
	// either returns or says so promptly; it never hangs or steals focus.
	run("open", map[string]interface{}{"url": site.URL + "/"})
	extension(fmt.Sprintf(`chrome.windows.update(%d, { state: "minimized" }).then(() => ({}))`, agentWindow))
	minimized := func() bool {
		t.Helper()
		return extension(fmt.Sprintf(`chrome.windows.get(%d).then((w) => ({ state: w.state }))`, agentWindow))["state"] == "minimized"
	}
	// macOS animates the minimize; wait for the page to see it.
	hidden := extension(fmt.Sprintf(`(async () => {
		const [g] = await chrome.tabGroups.query({ title: %q });
		const [tab] = await chrome.tabs.query({ groupId: g.id });
		let visibility = "";
		for (let i = 0; i < 60 && visibility !== "hidden"; i++) {
			const [frame] = await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: () => document.visibilityState });
			visibility = frame.result;
			if (visibility !== "hidden") await new Promise((r) => setTimeout(r, 50));
		}
		return { visibility };
	})()`, "Orchestrator · "+session))
	if hidden["visibility"] != "hidden" {
		t.Fatalf("minimized agent page visibility = %v, want hidden", hidden["visibility"])
	}
	run("act", map[string]interface{}{"instruction": "email", "action": "fill", "value": "hidden@example.com"})
	if tier := inputTier(run("act", map[string]interface{}{"instruction": "save button"})); tier != "trusted" {
		t.Fatalf("input tier in a minimized window = %v", tier)
	}
	run("wait", map[string]interface{}{"text": "Saved hidden@example.com", "timeoutMs": 5000})
	started := time.Now()
	if _, err := broker.Execute(ctx, session, "screenshot", nil); err != nil {
		var commandErr browserruntime.CommandError
		if !errorAs(err, &commandErr) || commandErr.Code != "SCREENSHOT_UNAVAILABLE" {
			t.Fatalf("minimized screenshot error = %v, want a frame or SCREENSHOT_UNAVAILABLE", err)
		}
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("minimized screenshot took %v", elapsed)
	}
	if !minimized() {
		t.Fatal("the agent restored its minimized window")
	}
	extension(fmt.Sprintf(`chrome.windows.update(%d, { state: "normal" }).then(() => ({}))`, agentWindow))

	// A second session gets its own window and doesn't disturb the first.
	const second = "e2e-second"
	secondConsent := make(chan error, 1)
	go func() { secondConsent <- clickConsent(ctx, cdp) }()
	if _, err := broker.Execute(ctx, second, "open", map[string]interface{}{"url": site.URL + "/next"}); err != nil {
		t.Fatalf("second session open: %v", err)
	}
	if err := <-secondConsent; err != nil {
		t.Fatalf("second consent: %v", err)
	}
	if windowOf("Orchestrator · "+second) != agentWindow {
		t.Fatal("the second session opened a window instead of joining the user's")
	}
	if err := broker.DestroySession(ctx, second); err != nil {
		t.Fatalf("destroy second: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if left := extension(fmt.Sprintf(`Promise.all([chrome.windows.getAll(), chrome.tabGroups.query({ title: %q })]).then(([all, groups]) => ({ open: all.some((w) => w.id === %d), groups: groups.length }))`, "Orchestrator · "+second, agentWindow)); left["open"] != true || left["groups"].(float64) != 0 {
		t.Fatalf("after the second session ended: %v, want the user's window open and its group gone", left)
	}

	// Allowing every session: the next new session runs without asking.
	const third, fourth = "e2e-third", "e2e-fourth"
	allConsent := make(chan error, 1)
	go func() {
		allConsent <- clickConsentExpression(ctx, cdp, "document.getElementById('all-sessions').click(); document.getElementById('allow').click()")
	}()
	if _, err := broker.Execute(ctx, third, "open", map[string]interface{}{"url": site.URL + "/next"}); err != nil {
		t.Fatalf("third session open: %v", err)
	}
	if err := <-allConsent; err != nil {
		t.Fatalf("allow every session: %v", err)
	}
	if _, err := broker.Execute(ctx, fourth, "open", map[string]interface{}{"url": site.URL + "/next"}); err != nil {
		t.Fatalf("session after allowing every session: %v", err)
	}
	if err := broker.DestroySession(ctx, third); err != nil {
		t.Fatalf("destroy third: %v", err)
	}
	if err := broker.DestroySession(ctx, fourth); err != nil {
		t.Fatalf("destroy fourth: %v", err)
	}

	// The user, with another window open, closes the one holding the agent's
	// group: the next command starts over cleanly in the window they use now.
	// (Closing a profile's last window on macOS shuts the profile down.)
	other := int(extension(`chrome.windows.create({ focused: true, url: "about:blank" }).then((w) => ({ id: w.id }))`)["id"].(float64))
	time.Sleep(500 * time.Millisecond)
	extension(fmt.Sprintf(`chrome.windows.remove(%d).then(() => ({}))`, agentWindow))
	time.Sleep(500 * time.Millisecond)
	reopened := run("open", map[string]interface{}{"url": site.URL + "/next"})
	if reopened["isLoading"] == nil {
		t.Fatalf("open after closing the window = %#v", reopened)
	}
	if got := windowOf("Orchestrator · " + session); got != other {
		t.Fatalf("session window after the user closed theirs = %d, want the user's other window %d", got, other)
	}

	if err := broker.DestroySession(ctx, session); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

// evalInExtension runs an expression in the extension's service worker. It must
// resolve to a JSON object.
func evalInExtension(ctx context.Context, cdp, expression string) (map[string]interface{}, error) {
	resp, err := http.Get(cdp + "/json/list")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var targets []struct {
		Type                 string `json:"type"`
		URL                  string `json:"url"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target.Type != "service_worker" || !strings.Contains(target.URL, ExtensionID) {
			continue
		}
		conn, _, err := websocket.Dial(ctx, target.WebSocketDebuggerURL, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		payload, _ := json.Marshal(map[string]interface{}{
			"id":     1,
			"method": "Runtime.evaluate",
			"params": map[string]interface{}{"expression": expression, "awaitPromise": true, "returnByValue": true},
		})
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return nil, err
		}
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return nil, err
			}
			var message struct {
				ID     int `json:"id"`
				Result struct {
					Result struct {
						Value map[string]interface{} `json:"value"`
					} `json:"result"`
					ExceptionDetails *struct {
						Exception struct {
							Description string `json:"description"`
						} `json:"exception"`
					} `json:"exceptionDetails"`
				} `json:"result"`
			}
			if json.Unmarshal(data, &message) != nil || message.ID != 1 {
				continue
			}
			if details := message.Result.ExceptionDetails; details != nil {
				return nil, errors.New(details.Exception.Description)
			}
			return message.Result.Result.Value, nil
		}
	}
	return nil, errors.New("the extension's service worker is not running")
}

// inputTier reads which input tier an act result's dispatch used.
func inputTier(act map[string]interface{}) interface{} {
	result, _ := act["result"].(map[string]interface{})
	return result["inputTier"]
}

func errorAs(err error, target *browserruntime.CommandError) bool {
	return errors.As(err, target)
}

// clickConsent waits for the extension's consent window and clicks Allow
// through the Chrome DevTools Protocol, standing in for the person.
func clickConsent(ctx context.Context, cdp string) error {
	return clickConsentExpression(ctx, cdp, "document.getElementById('allow').click()")
}

func clickConsentExpression(ctx context.Context, cdp, expression string) error {
	// Run the clicks only once consent.js has attached its listeners: the
	// target appears before the page's script runs.
	expression = `new Promise((resolve) => {
		const go = () => {
			if (document.getElementById('allow') && document.readyState === 'complete') { ` + expression + `; resolve(true); } else setTimeout(go, 50);
		};
		go();
	})`
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
						"params": map[string]interface{}{"expression": expression, "userGesture": true, "awaitPromise": true},
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
