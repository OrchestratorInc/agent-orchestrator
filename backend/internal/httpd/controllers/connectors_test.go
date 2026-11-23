package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/connectors"
)

func newConnectorTestServer(t *testing.T, lan bool) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Connectors: connectors.New(t.TempDir(), "http://127.0.0.1:3001")}, httpd.ControlDeps{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lan {
			r = r.WithContext(requestscope.WithLAN(r.Context()))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func connectorCall(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestConnectorRoutesHideStoredValues(t *testing.T) {
	srv := newConnectorTestServer(t, false)
	status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"Docs","transport":"http","url":"https://docs.example.com/mcp?api_key=in-the-query","auth":"header","headers":{"Authorization":"Bearer hidden-value"}}`)
	if status != http.StatusCreated || !strings.Contains(body, `"key":"docs"`) || !strings.Contains(body, `"headerKeys":["Authorization"]`) {
		t.Fatalf("create = %d %s", status, body)
	}
	for _, path := range []string{"/api/v1/connectors", "/api/v1/connectors/docs"} {
		status, body := connectorCall(t, srv, http.MethodGet, path, "")
		if status != http.StatusOK || strings.Contains(body, "hidden-value") || strings.Contains(body, "in-the-query") {
			t.Fatalf("GET %s = %d %s", path, status, body)
		}
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/docs", `{"enabled":false}`); status != http.StatusOK || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("disable = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"Docs","transport":"http","url":"https://other.example.com/mcp"}`); status != http.StatusConflict {
		t.Fatalf("duplicate = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"x","transport":"http","url":"https://x.example/mcp","unknown":1}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", status, body)
	}
	if status, _ := connectorCall(t, srv, http.MethodDelete, "/api/v1/connectors/docs", ""); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _ := connectorCall(t, srv, http.MethodGet, "/api/v1/connectors/docs", ""); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d", status)
	}
}

func TestConnectorCommandsNeedTheLoopbackListener(t *testing.T) {
	srv := newConnectorTestServer(t, true)
	status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"paper","transport":"stdio","command":"npx"}`)
	if status != http.StatusForbidden || !strings.Contains(body, "CONNECTOR_COMMAND_LOOPBACK_REQUIRED") {
		t.Fatalf("stdio over LAN = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"linear","transport":"http","url":"https://mcp.linear.app/mcp","auth":"oauth"}`); status != http.StatusCreated {
		t.Fatalf("http over LAN = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/linear", `{"name":"linear","transport":"stdio","command":"npx"}`); status != http.StatusForbidden {
		t.Fatalf("change to stdio over LAN = %d %s", status, body)
	}
}

type stubSignIn struct{ *connectors.Service }

func (stubSignIn) StartSignIn(context.Context, string) (string, error) {
	return "https://auth.example.com/authorize?state=s1", nil
}

func (stubSignIn) CompleteSignIn(_ context.Context, state, code, _, refusal string) (connectors.Connector, error) {
	switch {
	case state == "s1" && code != "":
		return connectors.Connector{Name: "Linear <b>"}, nil
	case state == "s1":
		return connectors.Connector{}, apierr.Invalid("CONNECTOR_SIGNIN_REFUSED", "The server refused the sign-in: "+refusal, nil)
	default:
		return connectors.Connector{}, apierr.NotFound("CONNECTOR_SIGNIN_NOT_FOUND", "This sign-in is not open on this machine")
	}
}

func TestConnectorSignInRoutesAndCallbackPage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := stubSignIn{connectors.New(t.TempDir(), "http://127.0.0.1:3001")}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Connectors: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"linear","transport":"http","url":"https://mcp.linear.app/mcp","auth":"oauth"}`); status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}
	status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/linear/connect", "")
	if status != http.StatusOK || !strings.Contains(body, `"authorizationUrl":"https://auth.example.com/authorize?state=s1"`) ||
		!strings.Contains(body, `"callbackUrl":"http://127.0.0.1:3001/oauth/mcp/callback"`) {
		t.Fatalf("connect = %d %s", status, body)
	}
	for _, tt := range []struct {
		query  string
		status int
		want   string
	}{
		{"state=s1&code=c1", http.StatusOK, "Signed in to Linear &lt;b&gt;"},
		{"state=s1&error=access_denied", http.StatusBadRequest, "The server refused the sign-in: access_denied"},
		{"state=other&code=c1", http.StatusNotFound, "copy the address of this page and paste it in Orchestrator"},
	} {
		status, body := connectorCall(t, srv, http.MethodGet, "/oauth/mcp/callback?"+tt.query, "")
		if status != tt.status || !strings.Contains(body, tt.want) || strings.Contains(body, "<b>") {
			t.Errorf("callback %s = %d %s", tt.query, status, body)
		}
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/linear/connect/finish", `{"callbackUrl":"http://evil.example.com/oauth/mcp/callback?code=c&state=s1"}`); status != http.StatusBadRequest || !strings.Contains(body, "CONNECTOR_SIGNIN_ADDRESS") {
		t.Fatalf("finish with a wrong address = %d %s", status, body)
	}
	if !httpd.IsLANControlBlockedPathForTest("/oauth/mcp/callback") {
		t.Fatal("the sign-in callback must not answer on the remote listener")
	}
}

func TestConnectorProxyRouteIsMountedAndLoopbackOnly(t *testing.T) {
	srv := newConnectorTestServer(t, false)
	if status, body := connectorCall(t, srv, http.MethodPost, "/mcp/s/ao-1/connectors/linear", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); status != http.StatusUnauthorized {
		t.Fatalf("proxy without a session token = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/connectors", ""); status != http.StatusOK || !strings.Contains(body, `"connectors":[]`) {
		t.Fatalf("session connectors = %d %s", status, body)
	}
	if !httpd.IsLANControlBlockedPathForTest("/mcp/s/ao-1/connectors/linear") {
		t.Fatal("the agent proxy must not answer on the remote listener")
	}
}

func TestConnectorImportRoutesHideValuesAndKeepCommandsOnLoopback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{
		"docs":{"type":"http","url":"https://docs.example.com/mcp?key=in-the-query","headers":{"Authorization":"Bearer hidden-value"}},
		"files":{"command":"npx","args":["-y","files"],"env":{"TOKEN":"hidden-env"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ids := func(srv *httptest.Server) map[string]string {
		status, body := connectorCall(t, srv, http.MethodGet, "/api/v1/connectors/import", "")
		if status != http.StatusOK || strings.Contains(body, "hidden-") || strings.Contains(body, "in-the-query") {
			t.Fatalf("candidates = %d %s", status, body)
		}
		var out struct {
			Candidates []struct{ ID, Key string }
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Candidates) != 2 {
			t.Fatalf("candidates = %s (%v)", body, err)
		}
		return map[string]string{out.Candidates[0].Key: out.Candidates[0].ID, out.Candidates[1].Key: out.Candidates[1].ID}
	}
	lan := newConnectorTestServer(t, true)
	found := ids(lan)
	if status, body := connectorCall(t, lan, http.MethodPost, "/api/v1/connectors/import", `{"ids":["`+found["files"]+`"]}`); status != http.StatusForbidden ||
		!strings.Contains(body, "CONNECTOR_COMMAND_LOOPBACK_REQUIRED") {
		t.Fatalf("command import over LAN = %d %s", status, body)
	}
	local := newConnectorTestServer(t, false)
	status, body := connectorCall(t, local, http.MethodPost, "/api/v1/connectors/import", `{"ids":["`+found["files"]+`","`+found["docs"]+`"]}`)
	if status != http.StatusCreated || !strings.Contains(body, `"key":"files"`) || !strings.Contains(body, `"key":"docs"`) || strings.Contains(body, "hidden-") {
		t.Fatalf("import = %d %s", status, body)
	}
	if status, body := connectorCall(t, local, http.MethodPost, "/api/v1/connectors/import", `{"ids":["missing"]}`); status != http.StatusNotFound {
		t.Fatalf("unknown candidate = %d %s", status, body)
	}
	if status, body := connectorCall(t, local, http.MethodPost, "/api/v1/connectors", `{"name":"import","transport":"http","url":"https://x.example/mcp"}`); status != http.StatusBadRequest {
		t.Fatalf("reserved key = %d %s", status, body)
	}
}
