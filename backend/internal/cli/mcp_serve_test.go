package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const (
	testMCPPublic   = "https://ao-mcp.example.dev"
	testMCPCallback = "https://claude.ai/api/mcp/auth_callback"
	testMCPVerifier = "verifier-verifier-verifier-verifier-verifier-1234"
)

type mcpServeHarness struct {
	t      *testing.T
	srv    *mcpServeServer
	h      http.Handler
	spawns []string
	mu     sync.Mutex
}

// newMCPServeHarness wires the server to a fake Google that signs in as email
// and a fake daemon that knows one project and one chat agent.
func newMCPServeHarness(t *testing.T, email string) *mcpServeHarness {
	t.Helper()
	cfg := setConfigEnv(t)
	t.Setenv("AO_MCP_GOOGLE_CLIENT_ID", "gid")
	t.Setenv("AO_MCP_GOOGLE_CLIENT_SECRET", "gsecret")
	hs := &mcpServeHarness{t: t}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
			_, _ = io.WriteString(w, `{"projects":[{"id":"demo","name":"Demo","kind":"git","path":"/secret/path"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-1","projectId":"demo","kind":"worker","mode":"chat"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1/conversation":
			// Two pages: the newest holds a steer, stored as a system activity.
			if r.URL.Query().Get("beforeSequence") == "20" {
				_, _ = io.WriteString(w, `{"messages":[{"sequence":10,"role":"user","text":"task"},{"sequence":12,"role":"assistant","text":"on it"}],"hasMoreBefore":false,"oldestSequence":10}`)
				return
			}
			_, _ = io.WriteString(w, `{"messages":[{"sequence":20,"role":"assistant","text":"working"},{"sequence":22,"role":"assistant","text":"STEERED: done"}],`+
				`"activities":[{"sequence":21,"activityKind":"system","detail":{"event":"steer","text":"change of plan"}},{"sequence":23,"activityKind":"command","detail":{}}],"hasMoreBefore":true,"oldestSequence":20}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/reports":
			_, _ = io.WriteString(w, `{"reports":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			body, _ := io.ReadAll(r.Body)
			hs.mu.Lock()
			hs.spawns = append(hs.spawns, string(body))
			hs.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"session":{"id":"demo-1","status":"working","displayName":"Fix it"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(daemon.Close)
	writeRunFileFor(t, cfg, daemon)
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.PostForm.Get("code") != "gcode" || r.PostForm.Get("client_secret") != "gsecret" {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"gtoken"}`)
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer gtoken" {
				http.Error(w, "bad", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"email": email, "email_verified": true})
		}
	}))
	t.Cleanup(google.Close)
	c := &commandContext{deps: Deps{Out: io.Discard, Err: io.Discard, ProcessAlive: func(int) bool { return true }}.withDefaults()}
	srv, err := newMCPServeServer(c, mcpServeOptions{addr: "127.0.0.1:0", publicURL: testMCPPublic, allowEmails: []string{"Harshit@Example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	srv.googleAuthURL, srv.googleTokenURL, srv.googleUserURL = "https://accounts.example/auth", google.URL+"/token", google.URL+"/userinfo"
	hs.srv, hs.h = srv, srv.handler()
	return hs
}

func (hs *mcpServeHarness) do(method, target string, body io.Reader, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func (hs *mcpServeHarness) register(redirect string) (*httptest.ResponseRecorder, string) {
	rec := hs.do(http.MethodPost, "/register", strings.NewReader(`{"redirect_uris":["`+redirect+`"],"client_name":"Claude"}`), nil)
	var out struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out.ClientID
}

// signIn runs authorize and the Google callback, returning Claude's redirect.
func (hs *mcpServeHarness) signIn(clientID string) *url.URL {
	hs.t.Helper()
	sum := sha256.Sum256([]byte(testMCPVerifier))
	rec := hs.do(http.MethodGet, "/authorize?"+url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {testMCPCallback},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"claude-state"}, "resource": {testMCPPublic + "/mcp"},
	}.Encode(), nil, nil)
	google, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || google.Host != "accounts.example" || google.Query().Get("redirect_uri") != testMCPPublic+"/oauth/google/callback" {
		hs.t.Fatalf("authorize = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = hs.do(http.MethodGet, "/oauth/google/callback?"+url.Values{"code": {"gcode"}, "state": {google.Query().Get("state")}}.Encode(), nil, nil)
	back, _ := url.Parse(rec.Header().Get("Location"))
	return back
}

func (hs *mcpServeHarness) token(form url.Values) (int, map[string]any) {
	rec := hs.do(http.MethodPost, "/token", strings.NewReader(form.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (hs *mcpServeHarness) rpc(token, method string, params any) map[string]any {
	hs.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	rec := hs.do(http.MethodPost, "/mcp", strings.NewReader(string(body)), map[string]string{"Authorization": "Bearer " + token})
	if rec.Code != http.StatusOK {
		hs.t.Fatalf("%s = %d %s", method, rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestMCPServeSignsInThroughGoogleAndRunsTools(t *testing.T) {
	hs := newMCPServeHarness(t, "harshit@example.com")

	rec := hs.do(http.MethodPost, "/mcp", strings.NewReader(`{}`), nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), `resource_metadata="`+testMCPPublic+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("unauthenticated = %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	var prm map[string]any
	_ = json.Unmarshal(hs.do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil, nil).Body.Bytes(), &prm)
	if prm["resource"] != testMCPPublic+"/mcp" {
		t.Fatalf("protected resource metadata = %v", prm)
	}

	_, clientID := hs.register(testMCPCallback)
	back := hs.signIn(clientID)
	if back.Host != "claude.ai" || back.Query().Get("state") != "claude-state" || back.Query().Get("iss") != testMCPPublic || back.Query().Get("code") == "" {
		t.Fatalf("callback redirect = %s", back)
	}
	code := back.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {testMCPCallback}, "code_verifier": {testMCPVerifier}}
	status, tok := hs.token(form)
	if status != http.StatusOK || tok["access_token"] == nil {
		t.Fatalf("token = %d %v", status, tok)
	}
	if status, _ := hs.token(form); status != http.StatusBadRequest {
		t.Fatalf("reused code = %d, want 400", status)
	}
	access := tok["access_token"].(string)

	tools := hs.rpc(access, "tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 10 {
		t.Fatalf("tools = %d", len(tools))
	}
	projects := hs.rpc(access, "tools/call", map[string]any{"name": "list_projects", "arguments": map[string]any{}})
	if text, _ := json.Marshal(projects); strings.Contains(string(text), "/secret/path") || !strings.Contains(string(text), `"project_id":"demo"`) {
		t.Fatalf("list_projects = %s", text)
	}

	create := map[string]any{"name": "create_agent", "arguments": map[string]any{"request_id": "r1", "project": "demo", "prompt": "Fix it\nmore detail"}}
	first, second := hs.rpc(access, "tools/call", create), hs.rpc(access, "tools/call", create)
	if a, b := first["result"], second["result"]; a.(map[string]any)["isError"] != nil || !jsonEqual(a, b) {
		t.Fatalf("create twice = %v / %v", a, b)
	}
	if len(hs.spawns) != 1 || !strings.Contains(hs.spawns[0], `"clientRequestId":"mcp-`) || !strings.Contains(hs.spawns[0], `"displayName":"Fix it"`) {
		t.Fatalf("spawns = %v", hs.spawns)
	}
	create["arguments"].(map[string]any)["prompt"] = "Something else"
	if res := hs.rpc(access, "tools/call", create)["result"].(map[string]any); res["isError"] != true {
		t.Fatalf("reused request_id with new args = %v", res)
	}

	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {clientID}}
	if status, out := hs.token(refresh); status != http.StatusOK || out["refresh_token"] == tok["refresh_token"] {
		t.Fatalf("refresh = %d %v", status, out)
	}
	if status, _ := hs.token(refresh); status != http.StatusBadRequest {
		t.Fatalf("rotated refresh token reused = %d, want 400", status)
	}
}

func TestMCPServeRefusesAnEmailOffTheAllowlist(t *testing.T) {
	hs := newMCPServeHarness(t, "someone@example.com")
	_, clientID := hs.register(testMCPCallback)
	back := hs.signIn(clientID)
	if back.Host != "claude.ai" || back.Query().Get("error") != "access_denied" || back.Query().Get("code") != "" {
		t.Fatalf("callback redirect = %s", back)
	}
}

func TestMCPServeRejectsAWrongPKCEVerifier(t *testing.T) {
	hs := newMCPServeHarness(t, "harshit@example.com")
	_, clientID := hs.register(testMCPCallback)
	code := hs.signIn(clientID).Query().Get("code")
	status, out := hs.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {testMCPCallback}, "code_verifier": {testMCPVerifier + "x"}})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("token = %d %v", status, out)
	}
}

func TestMCPServeOnlyRegistersClaudeAndLoopbackRedirects(t *testing.T) {
	hs := newMCPServeHarness(t, "harshit@example.com")
	if rec, _ := hs.register("https://evil.example/cb"); rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign redirect = %d", rec.Code)
	}
	rec, clientID := hs.register("http://localhost/callback")
	if rec.Code != http.StatusCreated {
		t.Fatalf("loopback redirect = %d %s", rec.Code, rec.Body)
	}
	hs.srv.mu.Lock()
	registered := hs.srv.st.Clients[clientID].RedirectURIs
	hs.srv.mu.Unlock()
	if !mcpServeRedirectMatches(registered, "http://localhost:51234/callback") || mcpServeRedirectMatches(registered, "http://localhost:51234/other") {
		t.Fatal("loopback redirects must match on any port and only on the registered path")
	}
}

func TestMCPServeBindsLoopbackOnly(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_MCP_GOOGLE_CLIENT_ID", "gid")
	t.Setenv("AO_MCP_GOOGLE_CLIENT_SECRET", "gsecret")
	c := &commandContext{deps: Deps{}.withDefaults()}
	for _, addr := range []string{"0.0.0.0:8787", "192.168.1.5:8787", ":8787"} {
		if _, err := newMCPServeServer(c, mcpServeOptions{addr: addr, publicURL: testMCPPublic, allowEmails: []string{"a@b.c"}}); err == nil {
			t.Fatalf("addr %q accepted", addr)
		}
	}
}

// Claude's directory needs a title and read-only/destructive hints, and
// ChatGPT needs all three hints explicit; clients cache this list.
func TestMCPServeToolsDeclareEveryAnnotation(t *testing.T) {
	for _, tool := range mcpServeTools {
		ann := tool["annotations"].(map[string]any)
		for _, key := range []string{"title", "readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := ann[key]; !ok {
				t.Errorf("%s lacks %s", tool["name"], key)
			}
		}
	}
}

func TestMCPServeTranscriptFillsThePageAndShowsSteers(t *testing.T) {
	hs := newMCPServeHarness(t, "harshit@example.com")
	res := hs.srv.callTool(t.Context(), "get_transcript", json.RawMessage(`{"agent_id":"demo-1","limit":4}`))
	raw, _ := json.Marshal(res["structuredContent"])
	var out struct {
		Messages []mcpTranscriptItem `json:"messages"`
		More     bool                `json:"has_more_before"`
		Oldest   int64               `json:"oldest_sequence"`
	}
	_ = json.Unmarshal(raw, &out)
	var got []string
	for _, m := range out.Messages {
		got = append(got, m.Role+":"+m.UntrustedText)
	}
	want := "assistant:on it|assistant:working|user:change of plan|assistant:STEERED: done"
	if strings.Join(got, "|") != want || !out.Messages[2].Steer || !out.More || out.Oldest != 12 {
		t.Fatalf("transcript = %s", raw)
	}
	agent := hs.srv.callTool(t.Context(), "get_agent", json.RawMessage(`{"agent_id":"demo-1"}`))
	if raw, _ := json.Marshal(agent["structuredContent"]); !strings.Contains(string(raw), `"last_agent_message":{"sequence":22`) {
		t.Fatalf("get_agent = %s", raw)
	}
}

func TestMCPServeRejectsAnUnknownProject(t *testing.T) {
	hs := newMCPServeHarness(t, "harshit@example.com")
	for _, call := range []struct{ name, args string }{
		{"list_agents", `{"project":"demoo"}`},
		{"create_agent", `{"request_id":"r","project":"demoo","prompt":"x"}`},
	} {
		res := hs.srv.callTool(t.Context(), call.name, json.RawMessage(call.args))
		if raw, _ := json.Marshal(res); res["isError"] != true || !strings.Contains(string(raw), "call list_projects") {
			t.Fatalf("%s = %s", call.name, raw)
		}
	}
	if len(hs.spawns) != 0 {
		t.Fatalf("spawned for an unknown project: %v", hs.spawns)
	}
}

func TestMCPCleanErrorDropsRequestIDsAndCLIHints(t *testing.T) {
	for _, tc := range []struct {
		body apiError
		want string
	}{
		{apiError{Message: "Project x is not registered. Register it with `ao project add`.", Code: "PROJECT_NOT_FOUND", RequestID: "macos-2134.local/abc"}, "Project x is not registered."},
		{apiError{Message: "bad cursor", Code: "INVALID_SESSION_CURSOR", RequestID: "macos-2134.local/abc"}, "cursor is invalid or expired; call list_agents again without a cursor"},
		{apiError{RequestID: "macos-2134.local/abc"}, "AO could not complete the request (HTTP 400)"},
	} {
		if got := mcpCleanError(apiResponseError{StatusCode: http.StatusBadRequest, ErrorBody: tc.body}).Error(); got != tc.want {
			t.Errorf("clean(%+v) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
