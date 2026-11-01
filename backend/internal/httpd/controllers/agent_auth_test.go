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

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

type fakeAgentAuthService struct {
	plans       []agentauth.Plan
	startResult agentauth.StartResult
	startErr    error
	startedID   string
	startCalls  int
	uiResult    agentauth.LoginSnapshot
	uiMethod    string
	uiSecret    string
	uiModel     string
	uiOperation string
	uiCode      string
	codeCalls   int
}

func (f *fakeAgentAuthService) StartLogin(_ context.Context, agentID, method, secret string) (agentauth.LoginSnapshot, error) {
	f.startedID, f.uiMethod, f.uiSecret = agentID, method, secret
	f.startCalls++
	return f.uiResult, f.startErr
}

func (f *fakeAgentAuthService) StartLoginWithModel(ctx context.Context, agentID, method, secret, model string) (agentauth.LoginSnapshot, error) {
	f.uiModel = model
	return f.StartLogin(ctx, agentID, method, secret)
}

func (f *fakeAgentAuthService) Login(_ context.Context, agentID, id string) (agentauth.LoginSnapshot, error) {
	f.startedID, f.uiOperation = agentID, id
	return f.uiResult, f.startErr
}

func (f *fakeAgentAuthService) CancelLogin(ctx context.Context, agentID, id string) (agentauth.LoginSnapshot, error) {
	return f.Login(ctx, agentID, id)
}

func (f *fakeAgentAuthService) ActOnLogin(ctx context.Context, agentID, id, action string) (agentauth.LoginSnapshot, error) {
	f.uiMethod = action
	return f.Login(ctx, agentID, id)
}

func (f *fakeAgentAuthService) SubmitLoginCode(_ context.Context, agentID, id, code string) (agentauth.LoginSnapshot, error) {
	f.startedID, f.uiOperation, f.uiCode = agentID, id, code
	f.codeCalls++
	return f.uiResult, f.startErr
}

func TestAgentUILoginCodeRequestIsBoundedAndNeverReturnsCode(t *testing.T) {
	svc := &fakeAgentAuthService{uiResult: agentauth.LoginSnapshot{ID: "login-1", AgentID: "claude-code", Status: "running"}}
	server := newAgentAuthTestServer(t, svc)
	path := "/api/v1/agents/claude-code/auth/operations/login-1/code"
	for _, request := range []string{``, `{"code":`, `{"code":"one"} {"code":"two"}`, `{"code":"one","command":"arbitrary"}`, `{"code":"` + strings.Repeat("x", 21*1024) + `"}`} {
		_, status, _ := doRequest(t, server, http.MethodPost, path, request)
		if status != http.StatusBadRequest || svc.codeCalls != 0 {
			t.Fatalf("invalid request reached native input: status=%d calls=%d", status, svc.codeCalls)
		}
	}
	body, status, headers := doRequest(t, server, http.MethodPost, path, `{"code":"one-time-code#state"}`)
	if status != http.StatusAccepted || svc.uiCode != "one-time-code#state" || svc.startedID != "claude-code" || svc.uiOperation != "login-1" || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if strings.Contains(string(body), "one-time-code") {
		t.Fatal("code appeared in response")
	}
	svc.startErr = apierr.NotFound("AGENT_AUTH_OPERATION_NOT_FOUND", "Login operation not found.")
	body, status, _ = doRequest(t, server, http.MethodPost, path, `{"code":"one-time-code#state"}`)
	if status != http.StatusNotFound || !strings.Contains(string(body), `"requestId"`) || !strings.Contains(string(body), `"code":"AGENT_AUTH_OPERATION_NOT_FOUND"`) {
		t.Fatalf("missing operation envelope: status=%d body=%s", status, body)
	}
}

func TestAgentUILoginValidatesRequestBeforeStarting(t *testing.T) {
	for _, body := range []string{
		`{"method":"browser","command":"arbitrary"}`,
		`{"method":"browser"} {"method":"sso"}`,
		`{"method":`, ``,
		`{"method":"api_key","secret":"` + strings.Repeat("x", 21*1024) + `"}`,
	} {
		svc := &fakeAgentAuthService{}
		response, status, _ := doRequest(t, newAgentAuthTestServer(t, svc), http.MethodPost, "/api/v1/agents/codex/auth/operations", body)
		if status != http.StatusBadRequest || svc.startCalls != 0 || !strings.Contains(string(response), `"requestId"`) {
			t.Fatalf("status=%d calls=%d body=%s", status, svc.startCalls, response)
		}
	}
}

func TestAgentUILoginPassesCredentialWithoutReturningIt(t *testing.T) {
	svc := &fakeAgentAuthService{uiResult: agentauth.LoginSnapshot{ID: "login-1", AgentID: "codex", Method: "api_key", Status: "running"}}
	server := newAgentAuthTestServer(t, svc)
	body, status, headers := doRequest(t, server, http.MethodPost, "/api/v1/agents/codex/auth/operations", `{"method":"api_key","secret":"test-secret"}`)
	if status != http.StatusAccepted || svc.uiSecret != "test-secret" || svc.uiMethod != "api_key" || svc.startedID != "codex" || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", status, headers, body)
	}
	if strings.Contains(string(body), "test-secret") {
		t.Fatal("returned secret")
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		_, status, headers := doRequest(t, server, method, "/api/v1/agents/codex/auth/operations/login-1", "")
		if status != http.StatusOK || svc.uiOperation != "login-1" || svc.startedID != "codex" || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("method=%s status=%d", method, status)
		}
	}
}

func TestAgentUILoginPassesModelWithoutReturningCredential(t *testing.T) {
	svc := &fakeAgentAuthService{uiResult: agentauth.LoginSnapshot{ID: "login-1", AgentID: "qwen", Method: "env_OPENAI_API_KEY", Status: "succeeded"}}
	body, status, _ := doRequest(t, newAgentAuthTestServer(t, svc), http.MethodPost, "/api/v1/agents/qwen/auth/operations", `{"method":"env_OPENAI_API_KEY","model":"gpt-4o","secret":"test-secret"}`)
	if status != http.StatusAccepted || svc.uiModel != "gpt-4o" || svc.uiSecret != "test-secret" || strings.Contains(string(body), "test-secret") {
		t.Fatalf("status=%d model=%q body=%s", status, svc.uiModel, body)
	}
}

func TestAgentUILoginActionsAreFixedAndScoped(t *testing.T) {
	svc := &fakeAgentAuthService{uiResult: agentauth.LoginSnapshot{ID: "login-1", AgentID: "goose", Method: "native", Status: "running", NativePrompt: "menu"}}
	server := newAgentAuthTestServer(t, svc)
	path := "/api/v1/agents/goose/auth/operations/login-1/actions"
	for _, body := range []string{`{"action":"escape"}`, `{"action":"select","input":"secret"}`, `{"action":"select"} {"action":"next"}`} {
		_, status, _ := doRequest(t, server, http.MethodPost, path, body)
		if status != http.StatusBadRequest || svc.uiMethod != "" {
			t.Fatalf("accepted action %q: %d", body, status)
		}
	}
	body, status, headers := doRequest(t, server, http.MethodPost, path, `{"action":"next"}`)
	if status != http.StatusOK || svc.uiMethod != "next" || svc.startedID != "goose" || svc.uiOperation != "login-1" || headers.Get("Cache-Control") != "no-store" || strings.Contains(string(body), "secret") {
		t.Fatalf("status=%d method=%q body=%s", status, svc.uiMethod, body)
	}
	svc.uiMethod = ""
	body, status, _ = doRequest(t, server, http.MethodPost, path, `{"action":"2"}`)
	if status != http.StatusOK || svc.uiMethod != "2" {
		t.Fatalf("numbered menu digit rejected: status=%d method=%q body=%s", status, svc.uiMethod, body)
	}
}

func TestAgentUILoginErrorsPreserveEnvelope(t *testing.T) {
	svc := &fakeAgentAuthService{startErr: apierr.Conflict("AGENT_AUTH_IN_PROGRESS", "Login in progress", map[string]any{"operationId": "existing"})}
	body, status, _ := doRequest(t, newAgentAuthTestServer(t, svc), http.MethodPost, "/api/v1/agents/codex/auth/operations", `{"method":"browser"}`)
	if status != http.StatusConflict || !strings.Contains(string(body), `"code":"AGENT_AUTH_IN_PROGRESS"`) || !strings.Contains(string(body), `"requestId"`) || !strings.Contains(string(body), `"operationId":"existing"`) {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func (f *fakeAgentAuthService) Plans(context.Context) []agentauth.Plan { return f.plans }

func (f *fakeAgentAuthService) Start(_ context.Context, agentID string) (agentauth.StartResult, error) {
	f.startCalls++
	f.startedID = agentID
	return f.startResult, f.startErr
}

func newAgentAuthTestServer(t *testing.T, svc controllers.AgentAuthService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{AgentAuth: svc}, httpd.ControlDeps{}))
	t.Cleanup(server.Close)
	return server
}

func TestAgentAuthListReturnsDisplaySafePlans(t *testing.T) {
	svc := &fakeAgentAuthService{plans: []agentauth.Plan{{
		AgentID:          "codex",
		Action:           agentauth.ActionLogin,
		LaunchMode:       agentauth.LaunchTerminal,
		Available:        true,
		DisplayCommand:   "codex login",
		Guidance:         "Native browser flow",
		DocumentationURL: "https://developers.openai.com/codex/auth/",
	}}}
	server := newAgentAuthTestServer(t, svc)

	body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/agents/auth-plans", "")
	if status != http.StatusOK {
		t.Fatalf("GET auth-plans = %d, body=%s", status, body)
	}
	for _, want := range []string{`"agentId":"codex"`, `"action":"login"`, `"launchMode":"terminal"`, `"displayCommand":"codex login"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	for _, forbidden := range []string{"argv", "initialInput"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("body exposed %q: %s", forbidden, body)
		}
	}
}

func TestAgentAuthStartReturnsTerminalHandle(t *testing.T) {
	createdAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	svc := &fakeAgentAuthService{startResult: agentauth.StartResult{
		AgentID:  "pi",
		Action:   agentauth.ActionLogin,
		Guidance: "Native Pi login flow",
		Terminal: shellterm.ShellTerminal{HandleID: "shellterm-auth", Title: "Log in to Pi", WorkingDir: "/tmp/ao", CreatedAt: createdAt},
	}}
	server := newAgentAuthTestServer(t, svc)

	body, status, _ := doRequest(t, server, http.MethodPost, "/api/v1/agents/pi/auth", "")
	if status != http.StatusCreated {
		t.Fatalf("POST agent auth = %d, body=%s", status, body)
	}
	if svc.startedID != "pi" || svc.startCalls != 1 {
		t.Fatalf("Start calls = %d, id=%q", svc.startCalls, svc.startedID)
	}
	for _, want := range []string{`"agentId":"pi"`, `"handleId":"shellterm-auth"`, `"title":"Log in to Pi"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestAgentAuthStartIgnoresRequestBody(t *testing.T) {
	for _, body := range []string{`{"argv":["rm","-rf"],"token":"secret"}`, `{not json`} {
		t.Run(body, func(t *testing.T) {
			svc := &fakeAgentAuthService{startResult: agentauth.StartResult{AgentID: "codex", Action: agentauth.ActionLogin}}
			server := newAgentAuthTestServer(t, svc)

			_, status, _ := doRequest(t, server, http.MethodPost, "/api/v1/agents/codex/auth", body)
			if status != http.StatusCreated {
				t.Fatalf("POST agent auth = %d, want %d", status, http.StatusCreated)
			}
			if svc.startedID != "codex" || svc.startCalls != 1 {
				t.Fatalf("Start calls = %d, id=%q", svc.startCalls, svc.startedID)
			}
		})
	}
}

func TestAgentAuthErrorsPreserveEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{name: "unknown", code: "AGENT_AUTH_TARGET_UNKNOWN"},
		{name: "unavailable", code: "AGENT_AUTH_UNAVAILABLE"},
		{name: "instructions", code: "AGENT_AUTH_INSTRUCTIONS_ONLY"},
		{name: "documentation", code: "AGENT_AUTH_DOCUMENTATION_ONLY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeAgentAuthService{startErr: apierr.Invalid(tc.code, tc.name, nil)}
			server := newAgentAuthTestServer(t, svc)

			body, status, _ := doRequest(t, server, http.MethodPost, "/api/v1/agents/codex/auth", "")
			if status != http.StatusBadRequest || !strings.Contains(string(body), `"code":"`+tc.code+`"`) || !strings.Contains(string(body), `"requestId":"`) {
				t.Fatalf("POST agent auth = %d, body=%s", status, body)
			}
		})
	}
}

func TestAgentAuthNotImplementedWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	defer server.Close()

	for _, request := range []struct{ method, path string }{
		{method: http.MethodGet, path: "/api/v1/agents/auth-plans"},
		{method: http.MethodPost, path: "/api/v1/agents/codex/auth"},
		{method: http.MethodPost, path: "/api/v1/agents/codex/logout"},
		{method: http.MethodPost, path: "/api/v1/agents/codex/auth/operations"},
		{method: http.MethodGet, path: "/api/v1/agents/codex/auth/operations/login-1"},
		{method: http.MethodDelete, path: "/api/v1/agents/codex/auth/operations/login-1"},
		{method: http.MethodPost, path: "/api/v1/agents/codex/auth/operations/login-1/actions"},
	} {
		body, status, _ := doRequest(t, server, request.method, request.path, "")
		if status != http.StatusNotImplemented || !strings.Contains(string(body), `"code":"NOT_IMPLEMENTED"`) {
			t.Fatalf("%s %s = %d, body=%s", request.method, request.path, status, body)
		}
	}
}

func (f *fakeAgentAuthService) Logout(_ context.Context, agentID string) (agentauth.StartResult, error) {
	f.startCalls++
	f.startedID = agentID
	return f.startResult, f.startErr
}

func TestAgentLogoutRouteIgnoresClientCommandsAndPreservesErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{"success", nil, http.StatusCreated, `"action":"logout"`},
		{"unsupported", apierr.Invalid("AGENT_LOGOUT_UNSUPPORTED", "unsupported", nil), http.StatusBadRequest, `"code":"AGENT_LOGOUT_UNSUPPORTED"`},
		{"terminal error", apierr.Internal("TERMINAL_FAILED", "failed"), http.StatusInternalServerError, `"code":"TERMINAL_FAILED"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeAgentAuthService{startResult: agentauth.StartResult{AgentID: "codex", Action: "logout"}, startErr: tc.err}
			body, status, _ := doRequest(t, newAgentAuthTestServer(t, svc), http.MethodPost, "/api/v1/agents/codex/logout", `{"command":["arbitrary","command"]}`)
			if status != tc.status || !strings.Contains(string(body), tc.want) || svc.startedID != "codex" || svc.startCalls != 1 {
				t.Fatalf("status=%d body=%s calls=%d id=%s", status, body, svc.startCalls, svc.startedID)
			}
			if tc.err != nil && !strings.Contains(string(body), `"requestId":"`) {
				t.Fatalf("missing request id: %s", body)
			}
		})
	}
}
