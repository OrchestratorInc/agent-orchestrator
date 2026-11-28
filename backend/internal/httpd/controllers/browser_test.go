package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/browserruntime"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/config"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
	browsersvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/browser"
)

type fakeBrowserRuntime struct {
	status browserruntime.Status
	action string
	args   map[string]interface{}
	err    error
}

func (f *fakeBrowserRuntime) Status(_ context.Context, _ domain.SessionID, _ string) (browserruntime.Status, error) {
	return f.status, nil
}

func (f *fakeBrowserRuntime) Execute(
	_ context.Context,
	_ domain.SessionID,
	_ string,
	action string,
	args map[string]interface{},
) (browserruntime.Result, string, error) {
	f.action, f.args = action, args
	if action == "eval" {
		return browserruntime.Result{}, action, apierr.Invalid(
			"BROWSER_ACTION_UNSUPPORTED",
			"Unsupported browser action",
			nil,
		)
	}
	if f.err != nil {
		return browserruntime.Result{}, action, f.err
	}
	return browserruntime.Result{RequestID: "request-1", Value: map[string]interface{}{"text": "button Save [ref=e1]"}}, action, nil
}

func (f *fakeBrowserRuntime) ExecuteRemote(
	_ context.Context,
	_ domain.SessionID,
	_ string,
	args []string,
	_ map[string]string,
) (browsersvc.RemoteResponse, error) {
	f.action = "remote:" + strings.Join(args, " ")
	if f.err != nil {
		return browsersvc.RemoteResponse{}, f.err
	}
	return browsersvc.RemoteResponse{Output: "clicked", Files: map[string]string{"/tmp/s.png": "cG5n"}}, nil
}

func TestBrowserRemoteCommandRoute(t *testing.T) {
	runtime := &fakeBrowserRuntime{}
	srv := browserServer(t, runtime)
	post := func(body string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/browser/remote-commands", strings.NewReader(body))
		req.Header.Set("X-AO-Browser-Capability", "cap")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(raw)
	}
	resp, body := post(`{"sessionId":"s1","args":["click","@e2"]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"output":"clicked"`) || runtime.action != "remote:click @e2" {
		t.Fatalf("status %d body %s action %q", resp.StatusCode, body, runtime.action)
	}
	if resp, _ := post(`{"sessionId":"s1","args":[]}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty args status %d", resp.StatusCode)
	}
	runtime.err = apierr.Forbidden("BROWSER_ACTION_DENIED", "The user denied this action")
	if resp, body := post(`{"sessionId":"s1","args":["click","@e2"]}`); resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "BROWSER_ACTION_DENIED") {
		t.Fatalf("deny status %d body %s", resp.StatusCode, body)
	}
	runtime.err = apierr.Unavailable("BROWSER_REMOTE_UNREACHABLE", "down")
	if resp, _ := post(`{"sessionId":"s1","args":["snapshot"]}`); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unreachable status %d", resp.StatusCode)
	}
}

func browserServer(t *testing.T, runtime *fakeBrowserRuntime) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: newFakeSessionService(),
		Browser:  runtime,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrowserStatusAndSnapshot(t *testing.T) {
	connectedAt := time.Now().UTC().Truncate(time.Second)
	runtime := &fakeBrowserRuntime{status: browserruntime.Status{Connected: true, ConnectedAt: connectedAt}}
	srv := browserServer(t, runtime)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/browser/status?sessionId=ao-1", "")
	if status != http.StatusOK || !containsAll(body, `"connected":true`, `"transport":"electron-webcontents-debugger"`) {
		t.Fatalf("status = %d body=%s", status, body)
	}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"snapshot","args":{"interactive":true}}`)
	if status != http.StatusOK || !containsAll(body, `"requestId":"request-1"`, `"button Save [ref=e1]"`) {
		t.Fatalf("command = %d body=%s", status, body)
	}
	if runtime.action != "snapshot" || runtime.args["interactive"] != true {
		t.Fatalf("runtime command = %q %#v", runtime.action, runtime.args)
	}
}

func TestBrowserCoreInteractionActionsReachRuntime(t *testing.T) {
	runtime := &fakeBrowserRuntime{}
	srv := browserServer(t, runtime)
	actions := []string{
		"act", "type", "press", "hover", "highlight", "unhighlight",
		"tabs", "tab-new", "tab-select", "tab-close",
		"scroll", "select", "check", "uncheck", "get",
		"network-start", "network-status", "network-list", "network-stop", "network-clear",
		"devtools-open", "devtools-close",
	}

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			body, status, _ := doRequest(
				t,
				srv,
				http.MethodPost,
				"/api/v1/browser/commands",
				`{"sessionId":"ao-1","action":"`+action+`","args":{"probe":true}}`,
			)
			if status != http.StatusOK {
				t.Fatalf("%s = %d body=%s", action, status, body)
			}
			if runtime.action != action || runtime.args["probe"] != true {
				t.Fatalf("runtime command = %q %#v", runtime.action, runtime.args)
			}
		})
	}
}

func TestBrowserCommandValidationAndErrors(t *testing.T) {
	runtime := &fakeBrowserRuntime{}
	srv := browserServer(t, runtime)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"eval"}`)
	if status != http.StatusBadRequest || !containsAll(body, `"code":"BROWSER_ACTION_UNSUPPORTED"`) {
		t.Fatalf("unsupported = %d body=%s", status, body)
	}
	runtime.err = browserruntime.ErrUnavailable
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"snapshot"}`)
	if status != http.StatusServiceUnavailable || !containsAll(body, `"code":"BROWSER_RUNTIME_UNAVAILABLE"`) {
		t.Fatalf("unavailable = %d body=%s", status, body)
	}
	runtime.err = browserruntime.CommandError{Code: "STALE_REFERENCE", Message: "snapshot again"}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"click"}`)
	if status != http.StatusConflict || !containsAll(body, `"code":"STALE_REFERENCE"`) {
		t.Fatalf("stale = %d body=%s", status, body)
	}
	runtime.err = browserruntime.CommandError{Code: "BROWSER_TARGET_MISMATCH", Message: "Browser automation could not target AO tab t2"}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"click"}`)
	if status != http.StatusConflict || !containsAll(body, `"code":"BROWSER_TARGET_MISMATCH"`, `"message":"Browser automation could not target AO tab t2"`) {
		t.Fatalf("target mismatch = %d body=%s", status, body)
	}
	runtime.err = browserruntime.CommandError{Code: "AGENT_BROWSER_TIMEOUT", Message: "agent-browser command timed out"}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"screenshot"}`)
	if status != http.StatusGatewayTimeout || !containsAll(body, `"code":"AGENT_BROWSER_TIMEOUT"`) {
		t.Fatalf("native timeout = %d body=%s", status, body)
	}
	runtime.err = context.DeadlineExceeded
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"ao-1","action":"screenshot"}`)
	if status != http.StatusGatewayTimeout || !containsAll(body, `"code":"BROWSER_COMMAND_TIMEOUT"`) {
		t.Fatalf("request deadline = %d body=%s", status, body)
	}
}

func containsAll(body []byte, parts ...string) bool {
	value := string(body)
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
