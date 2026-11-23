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
	if strings.HasPrefix(path, "/oauth/mcp/callback") || strings.HasSuffix(path, "#from-another-site") {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", "document")
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
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"Scoped Docs","key":"scoped","projectId":"p1","transport":"http","url":"https://scoped.example.com/mcp"}`); status != http.StatusCreated {
		t.Fatalf("create scoped = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/scoped", `{"transport":"http","url":"https://scoped.example.com/v2/mcp"}`); status != http.StatusOK ||
		!strings.Contains(body, `"projectId":"p1"`) || !strings.Contains(body, `"name":"Scoped Docs"`) || !strings.Contains(body, `/v2/mcp`) {
		t.Fatalf("an edit that leaves the project and the name out = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/scoped", `{"transport":"http","url":"https://scoped.example.com/v2/mcp","projectId":""}`); status != http.StatusOK || strings.Contains(body, `"projectId":"p1"`) {
		t.Fatalf("an edit to all projects = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"own","transport":"http","url":"https://own.example.com/mcp","auth":"oauth","oauthClientId":"client-9","oauthClientSecret":"hidden-secret"}`); status != http.StatusCreated {
		t.Fatalf("create with an own client = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/own", `{"name":"Own (work)"}`); status != http.StatusOK ||
		!strings.Contains(body, `"oauthClientId":"client-9"`) || !strings.Contains(body, `"name":"Own (work)"`) || !strings.Contains(body, `"auth":"oauth"`) {
		t.Fatalf("an edit of the name only = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/own", `{"auth":"header","headers":{"Authorization":"Bearer hidden-value"}}`); status != http.StatusOK ||
		!strings.Contains(body, `"auth":"header"`) || strings.Contains(body, `client-9`) {
		t.Fatalf("an edit of the sign-in only = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/scoped", `{"enabled":false,"name":"Scoped v2"}`); status != http.StatusOK ||
		!strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"name":"Scoped v2"`) || !strings.Contains(body, `/v2/mcp`) {
		t.Fatalf("an edit of the name with a toggle = %d %s", status, body)
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
	if status, body := connectorCall(t, srv, http.MethodGet, "/api/v1/connectors#from-another-site", ""); status != http.StatusForbidden || !strings.Contains(body, "ORIGIN_FORBIDDEN") {
		t.Fatalf("a page of another site read the connectors = %d %s", status, body)
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
	if status, body := connectorCall(t, srv, http.MethodGet, "/mcp/s/ao-1/connectors/paper/launch", ""); status != http.StatusUnauthorized {
		t.Fatalf("launch details without a session token = %d %s", status, body)
	}
	for _, path := range []string{"/mcp/s/ao-1/connectors/linear", "/mcp/s/ao-1/connectors/paper/launch"} {
		if !httpd.IsLANControlBlockedPathForTest(path) {
			t.Fatalf("%s must not answer on the remote listener", path)
		}
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
	if !httpd.IsLANControlBlockedPathForTest("/api/v1/connectors/import") {
		t.Fatal("the import list reads this computer's agent configuration and must not answer on the remote listener")
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

func TestConnectorProposalRoutesAddNothingBeforeApproval(t *testing.T) {
	srv := newConnectorTestServer(t, false)
	status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/proposals",
		`{"sessionId":"ao-1","key":"paper","transport":"stdio","command":"npx","args":["-y","paper-mcp"],"envKeys":["PAPER_KEY"],"reason":"To read the design."}`)
	if status != http.StatusCreated || !strings.Contains(body, `"state":"pending"`) || !strings.Contains(body, `"envKeys":["PAPER_KEY"]`) {
		t.Fatalf("propose = %d %s", status, body)
	}
	var created struct {
		Proposal struct{ ID string }
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.Proposal.ID == "" {
		t.Fatalf("proposal = %s (%v)", body, err)
	}
	id := created.Proposal.ID
	if status, body := connectorCall(t, srv, http.MethodGet, "/api/v1/connectors", ""); status != http.StatusOK || !strings.Contains(body, `"connectors":[]`) {
		t.Fatalf("connectors after a proposal = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/connectors/proposals", ""); status != http.StatusOK || !strings.Contains(body, id) {
		t.Fatalf("session proposals = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/proposals/"+id+"/approve", `{}`); status != http.StatusBadRequest ||
		!strings.Contains(body, "CONNECTOR_PROPOSAL_VALUE") {
		t.Fatalf("approve without the value = %d %s", status, body)
	}
	lan := newConnectorTestServer(t, true)
	lanStatus, lanBody := connectorCall(t, lan, http.MethodPost, "/api/v1/connectors/proposals",
		`{"sessionId":"ao-1","key":"paper","transport":"stdio","command":"npx"}`)
	var remote struct {
		Proposal struct{ ID string }
	}
	if lanStatus != http.StatusCreated || json.Unmarshal([]byte(lanBody), &remote) != nil {
		t.Fatalf("propose over LAN = %d %s", lanStatus, lanBody)
	}
	if status, body := connectorCall(t, lan, http.MethodPost, "/api/v1/connectors/proposals/"+remote.Proposal.ID+"/approve", `{}`); status != http.StatusForbidden ||
		!strings.Contains(body, "CONNECTOR_COMMAND_LOOPBACK_REQUIRED") {
		t.Fatalf("approve a command over LAN = %d %s", status, body)
	}
	status, body = connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/proposals/"+id+"/approve", `{"projectId":"","values":{"PAPER_KEY":"hidden-value"}}`)
	if status != http.StatusOK || !strings.Contains(body, `"state":"approved"`) || !strings.Contains(body, `"key":"paper"`) || strings.Contains(body, "hidden-value") {
		t.Fatalf("approve = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/proposals/"+id+"/decline", ``); status != http.StatusOK || !strings.Contains(body, `"state":"approved"`) {
		t.Fatalf("decline after approval = %d %s", status, body)
	}
	if status, _ := connectorCall(t, srv, http.MethodGet, "/api/v1/connectors/proposals/none", ""); status != http.StatusNotFound {
		t.Fatalf("unknown proposal = %d", status)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"proposals","transport":"http","url":"https://x.example/mcp"}`); status != http.StatusBadRequest {
		t.Fatalf("reserved key = %d %s", status, body)
	}
}

func TestARemoteClientCannotTurnOnAConnectorThatRunsACommand(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Connectors: connectors.New(t.TempDir(), "http://127.0.0.1:3001")}, httpd.ControlDeps{})
	remote := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remote {
			r = r.WithContext(requestscope.WithLAN(r.Context()))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"paper","transport":"stdio","command":"npx","enabled":false}`); status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}
	remote = true
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/paper", `{"enabled":true}`); status != http.StatusForbidden || !strings.Contains(body, "CONNECTOR_COMMAND_LOOPBACK_REQUIRED") {
		t.Fatalf("turn on from another machine = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors/paper/test", ""); status != http.StatusForbidden || !strings.Contains(body, "CONNECTOR_COMMAND_LOOPBACK_REQUIRED") {
		t.Fatalf("test from another machine = %d %s", status, body)
	}
	remote = false
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/paper", `{"enabled":true}`); status != http.StatusOK || !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("turn on from this machine = %d %s", status, body)
	}
	remote = true
	if status, body := connectorCall(t, srv, http.MethodPatch, "/api/v1/connectors/paper", `{"enabled":false}`); status != http.StatusOK || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("turn off from another machine = %d %s", status, body)
	}
}

func TestARemoteClientCannotAimAConnectorAtTheMachineItself(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Connectors: connectors.New(t.TempDir(), "http://127.0.0.1:3001")}, httpd.ControlDeps{})
	remote := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remote {
			r = r.WithContext(requestscope.WithLAN(r.Context()))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	local := `{"name":"local","transport":"http","url":"http://127.0.0.1:1/shutdown"}`
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", local); status != http.StatusCreated {
		t.Fatalf("create on the machine = %d %s", status, body)
	}
	if status, body := connectorCall(t, srv, http.MethodPost, "/api/v1/connectors", `{"name":"far","transport":"http","url":"https://far.example.com/mcp"}`); status != http.StatusCreated {
		t.Fatalf("create far = %d %s", status, body)
	}
	remote = true
	for name, call := range map[string][3]string{
		"create": {http.MethodPost, "/api/v1/connectors", `{"name":"aimed","transport":"http","url":"http://localhost:3011/shutdown"}`},
		"edit":   {http.MethodPatch, "/api/v1/connectors/far", `{"url":"http://127.0.0.1:3011/shutdown"}`},
		"test":   {http.MethodPost, "/api/v1/connectors/local/test", ""},
		"signin": {http.MethodPost, "/api/v1/connectors/local/connect", ""},
	} {
		if status, body := connectorCall(t, srv, call[0], call[1], call[2]); status != http.StatusForbidden || !strings.Contains(body, "CONNECTOR_LOOPBACK_REQUIRED") {
			t.Errorf("%s from another machine = %d %s", name, status, body)
		}
	}
}
