package httpd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/access"
	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

type gateFixture struct {
	t        *testing.T
	registry *access.Registry
	router   chi.Router
	bodies   []string
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	f := &gateFixture{t: t}
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	f.registry = registry
	r := chi.NewRouter()
	gate := &accessGate{
		registry: registry, routes: r, now: time.Now,
		sessionProject: func(_ context.Context, sessionID string) (string, bool) {
			project, ok := map[string]string{"api-1": "api", "web-1": "web"}[sessionID]
			return project, ok
		},
	}
	r.Use(caller.Middleware)
	r.Use(gate.middleware)
	ok := func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		f.bodies = append(f.bodies, string(body))
		w.WriteHeader(http.StatusOK)
	}
	r.Get("/healthz", ok)
	r.Get("/api/v1/sessions", ok)
	r.Post("/api/v1/sessions", ok)
	r.Post("/api/v1/orchestrators", ok)
	r.Post("/api/v1/sessions/{sessionId}/fork", ok)
	r.Get("/api/v1/projects/{id}", ok)
	r.Get("/api/v1/projects/{projectId}/cues", ok)
	r.Get("/api/v1/sessions/{sessionId}", ok)
	r.Delete("/api/v1/sessions/{sessionId}", ok)
	r.Post("/api/v1/sessions/{sessionId}/send", ok)
	r.Get("/api/v1/projects", ok)
	r.Delete("/api/v1/projects/{id}", ok)
	r.Get("/api/v1/settings", ok)
	r.Post("/api/v1/access/tokens", ok)
	r.Get("/api/v1/access/whoami", ok)
	r.Get("/api/v1/mobile/status", ok)
	r.Post("/api/v1/sessions/{sessionId}/activity", ok)
	f.router = r
	return f
}

func (f *gateFixture) token(name, level string, projects ...string) string {
	f.t.Helper()
	grant, err := access.NewGrant(level, projects, time.Time{})
	if err != nil {
		f.t.Fatal(err)
	}
	_, secret, err := f.registry.Create(name, grant, time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	return secret
}

type gateCall struct {
	method, path, body, token, session, authorization string
}

func (f *gateFixture) do(call gateCall) (status int, code, reason string) {
	f.t.Helper()
	req := httptest.NewRequest(call.method, call.path, strings.NewReader(call.body))
	if call.token != "" {
		req.Header.Set("Authorization", "Bearer "+call.token)
	}
	if call.authorization != "" {
		req.Header.Set("Authorization", call.authorization)
	}
	if call.session != "" {
		req.Header.Set(caller.Header, call.session)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var envelope struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	reason, _ = envelope.Details["reason"].(string)
	return w.Code, envelope.Code, reason
}

func TestGateLeavesCallersWithNoGrantAlone(t *testing.T) {
	f := newGateFixture(t)
	for _, call := range []gateCall{
		{method: http.MethodGet, path: "/api/v1/mobile/status"},
		{method: http.MethodPost, path: "/api/v1/access/tokens"},
		{method: http.MethodDelete, path: "/api/v1/projects/api", session: "api-1"},
		{method: http.MethodPost, path: "/api/v1/sessions/api-1/fork", session: "api-1"},
		{method: http.MethodPost, path: "/api/v1/sessions/api-1/activity", session: "api-1"},
	} {
		if status, _, _ := f.do(call); status != http.StatusOK {
			t.Errorf("%s %s: status %d, want 200", call.method, call.path, status)
		}
	}
}

func TestGateHoldsATokenToItsGrant(t *testing.T) {
	f := newGateFixture(t)
	read := f.token("reader", access.LevelRead)
	operateAPI := f.token("api-bot", access.LevelOperate, "api")
	manage := f.token("admin", access.LevelManage)
	operateA := f.token("a-bot", access.LevelOperate, "A")
	spawn := func(project string) string { return `{"projectId":"` + project + `","prompt":"hi"}` }

	for name, tc := range map[string]struct {
		call   gateCall
		status int
		code   string
		reason string
	}{
		"read lists sessions":                    {gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: read}, 200, "", ""},
		"read cannot send":                       {gateCall{method: http.MethodPost, path: "/api/v1/sessions/api-1/send", token: read}, 403, "GRANT_REFUSED", access.ReasonLevel},
		"probe is open":                          {gateCall{method: http.MethodGet, path: "/healthz", token: read}, 200, "", ""},
		"whoami is open":                         {gateCall{method: http.MethodGet, path: "/api/v1/access/whoami", token: read}, 200, "", ""},
		"route outside the catalog":              {gateCall{method: http.MethodGet, path: "/api/v1/mobile/status", token: manage}, 403, "GRANT_REFUSED", access.ReasonCatalog},
		"token cannot make tokens":               {gateCall{method: http.MethodPost, path: "/api/v1/access/tokens", token: manage}, 403, "GRANT_REFUSED", access.ReasonCatalog},
		"unknown route":                          {gateCall{method: http.MethodGet, path: "/api/v1/nope", token: manage}, 404, "ROUTE_NOT_FOUND", ""},
		"unknown token":                          {gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: access.TokenPrefix + "ff"}, 401, "AGENT_TOKEN_INVALID", ""},
		"limited: session in its project":        {gateCall{method: http.MethodPost, path: "/api/v1/sessions/api-1/send", token: operateAPI}, 200, "", ""},
		"limited: session elsewhere":             {gateCall{method: http.MethodPost, path: "/api/v1/sessions/web-1/send", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: unknown session":               {gateCall{method: http.MethodGet, path: "/api/v1/sessions/ghost", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: list needs a project":          {gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: list in its project":           {gateCall{method: http.MethodGet, path: "/api/v1/sessions?project=api", token: operateAPI}, 200, "", ""},
		"limited: every project":                 {gateCall{method: http.MethodGet, path: "/api/v1/projects", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: machine data":                  {gateCall{method: http.MethodGet, path: "/api/v1/settings", token: operateAPI}, 200, "", ""},
		"limited: spawn in its project":          {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("api"), token: operateAPI}, 200, "", ""},
		"limited: spawn elsewhere":               {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("web"), token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: spawn with no project":         {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"prompt":"hi"}`, token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"manage removes a project":               {gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", token: manage}, 200, "", ""},
		"limited: second spelling of key":        {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api","ProjectId":"web"}`, token: operateAPI}, 400, "GRANT_TARGET_UNREADABLE", ""},
		"limited: other spelling of key":         {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"PROJECTID":"api"}`, token: operateAPI}, 400, "GRANT_TARGET_UNREADABLE", ""},
		"limited: first of two values":           {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("api") + spawn("web"), token: operateAPI}, 200, "", ""},
		"limited: first value elsewhere":         {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("web") + spawn("api"), token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: cues of its project":           {gateCall{method: http.MethodGet, path: "/api/v1/projects/api/cues", token: operateAPI}, 200, "", ""},
		"limited: escaped slash in an id":        {gateCall{method: http.MethodGet, path: "/api/v1/projects/api%2Fcues", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: an id that only looks escaped": {gateCall{method: http.MethodGet, path: "/api/v1/projects/%2541", token: operateA}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: a body that is not JSON":       {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api"`, token: operateAPI}, 400, "GRANT_TARGET_UNREADABLE", ""},
		"a lower-case scheme is still the token": {gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", authorization: "bearer " + read}, 403, "GRANT_REFUSED", access.ReasonLevel},
		"spaces around the token":                {gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", authorization: "Bearer  " + read + " "}, 403, "GRANT_REFUSED", access.ReasonLevel},
		"operate starts an orchestrator":         {gateCall{method: http.MethodPost, path: "/api/v1/orchestrators", body: spawn("api"), token: operateAPI}, 200, "", ""},
		"orchestrator elsewhere":                 {gateCall{method: http.MethodPost, path: "/api/v1/orchestrators", body: spawn("web"), token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"a session's own hook":                   {gateCall{method: http.MethodPost, path: "/api/v1/sessions/api-1/activity", token: manage}, 403, "GRANT_REFUSED", access.ReasonCatalog},
		"token that names a session":             {gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", token: read, session: "api-1"}, 403, "GRANT_REFUSED", access.ReasonLevel},
	} {
		status, code, reason := f.do(tc.call)
		if status != tc.status || (tc.code != "" && code != tc.code) || reason != tc.reason {
			t.Errorf("%s: got %d %q %q, want %d %q %q", name, status, code, reason, tc.status, tc.code, tc.reason)
		}
	}
	if !slices.Contains(f.bodies, spawn("api")) {
		t.Errorf("the spawn handler must still read the whole body, got %q", f.bodies)
	}
}

func TestRevokedTokenIsRefusedAndASessionHasNoLimit(t *testing.T) {
	f := newGateFixture(t)
	operateAPI := f.token("api-bot", access.LevelOperate, "api")
	if status, _, _ := f.do(gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api"}`, token: operateAPI}); status != 200 {
		t.Fatalf("spawn: status %d", status)
	}
	if _, found, err := f.registry.Revoke("api-bot"); err != nil || !found {
		t.Fatalf("revoke: %v %v", found, err)
	}
	if status, code, _ := f.do(gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: operateAPI}); status != 401 || code != "AGENT_TOKEN_INVALID" {
		t.Errorf("a revoked token: %d %q", status, code)
	}
	if status, _, _ := f.do(gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", session: "api-1"}); status != 200 {
		t.Errorf("a session is a caller with no grant, whoever started it: status %d", status)
	}
}

func TestTokenCannotReachAPreviewOrigin(t *testing.T) {
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := access.NewGrant(access.LevelManage, nil, time.Time{})
	_, secret, err := registry.Create("bot", grant, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, APIDeps{Access: registry}, ControlDeps{})
	for name, build := range map[string]func(string, domain.SessionID, string) (string, error){
		"preview": previewutil.FileURL, "artifact": previewutil.ArtifactFileURL, "inline artifact": previewutil.InlineArtifactFileURL,
	} {
		raw, err := build("http://127.0.0.1:3001", "web-1", "index.html")
		if err != nil {
			t.Fatal(err)
		}
		origin, _ := url.Parse(raw)
		for _, token := range []string{secret, ""} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/access/whoami", http.NoBody)
			req.Host = origin.Host
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if refused := strings.Contains(w.Body.String(), "GRANT_REFUSED"); refused != (token != "") {
				t.Errorf("%s origin, token=%v: got %d %s; the origin serves a session's files by host, whatever the path, so a token must be refused there and a caller with no token must not be", name, token != "", w.Code, w.Body)
			}
		}
	}
}

func TestRealRouterHoldsEveryActionToItsLevel(t *testing.T) {
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, level := range access.Levels() {
		grant, _ := access.NewGrant(level, nil, time.Time{})
		if _, tokens[level], err = registry.Create(level, grant, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, APIDeps{Access: registry}, ControlDeps{})
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	for _, action := range access.Catalog() {
		need := slices.Index(access.Levels(), action.Level)
		for have, level := range access.Levels() {
			req := httptest.NewRequest(action.Method, parameter.ReplaceAllString(action.Path, "x"), strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+tokens[level])
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			body := w.Body.String()
			if refused := strings.Contains(body, "GRANT_REFUSED"); refused != (have < need) {
				t.Errorf("%s needs %s: a %s token got %d %s", action.ID, action.Level, level, w.Code, body)
			}
			if strings.Contains(body, "ROUTE_NOT_FOUND") {
				t.Errorf("%s: the router serves no %s %s", action.ID, action.Method, action.Path)
			}
		}
	}
}

func TestRealRouterGatesTokensAndServesWhoami(t *testing.T) {
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := access.NewGrant(access.LevelRead, []string{"api"}, time.Time{})
	_, secret, err := registry.Create("reader", grant, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, APIDeps{Access: registry}, ControlDeps{})
	call := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, http.NoBody)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	var who struct {
		Kind  string
		Name  string
		Grant *struct {
			Level    string
			Projects []string
		}
	}
	if w := call(http.MethodGet, "/api/v1/access/whoami", secret); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &who) != nil ||
		who.Kind != access.CallerToken || who.Name != "reader" || who.Grant == nil || who.Grant.Level != access.LevelRead || len(who.Grant.Projects) != 1 {
		t.Errorf("whoami with a token: %d %s", w.Code, w.Body)
	}
	who.Grant = nil
	if w := call(http.MethodGet, "/api/v1/access/whoami", ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &who) != nil || who.Kind != access.CallerUser || who.Grant != nil {
		t.Errorf("whoami with no token: %d %s", w.Code, w.Body)
	}
	if w := call(http.MethodGet, "/api/v1/sessions/x/workspace/file?path=.env", secret); w.Code != 403 {
		t.Errorf("a route outside the catalog: status %d, want 403", w.Code)
	}
	if w := call(http.MethodGet, "/mux", secret); w.Code != 403 && w.Code != 404 {
		t.Errorf("the terminal socket: status %d, want 403 or 404", w.Code)
	}
	if w := call(http.MethodPost, "/shutdown", secret); w.Code != 403 && w.Code != 404 {
		t.Errorf("shutdown: status %d, want 403 or 404", w.Code)
	}
	if w := call(http.MethodGet, "/api/v1/access/tokens", secret); w.Code != 403 {
		t.Errorf("token list with a token: status %d, want 403", w.Code)
	}

	var actions struct {
		Actions []struct {
			ID      string
			Summary string
			Allowed bool
		}
	}
	if w := call(http.MethodGet, "/api/v1/access/actions", secret); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &actions) != nil || len(actions.Actions) != len(access.Catalog()) {
		t.Fatalf("actions: %d %s", w.Code, w.Body)
	}
	for i, action := range actions.Actions {
		if action.Summary == "" {
			t.Errorf("%s has no summary: its route is not in the API spec", action.ID)
		}
		entry := access.Catalog()[i]
		if want := entry.Level == access.LevelRead && entry.Project != access.ProjectAll; action.Allowed != want {
			t.Errorf("%s: allowed = %v for a read grant limited to one project, want %v", action.ID, action.Allowed, want)
		}
	}
	var detail struct {
		Action struct {
			Parameters []map[string]any
			Body       map[string]any
		}
	}
	if w := call(http.MethodGet, "/api/v1/access/actions/session.spawn", secret); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil {
		t.Fatalf("action detail: %d %s", w.Code, w.Body)
	}
	properties, _ := detail.Action.Body["properties"].(map[string]any)
	if _, ok := properties["projectId"]; !ok {
		t.Errorf("session.spawn body schema has no projectId: %v", detail.Action.Body)
	}
}

type connectSink struct{ events int }

func (s *connectSink) Emit(context.Context, ports.TelemetryEvent) { s.events++ }
func (s *connectSink) Close(context.Context) error                { return nil }

func TestRemoteListenerAcceptsAnAgentTokenAndKeepsItGated(t *testing.T) {
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := access.NewGrant(access.LevelRead, nil, time.Time{})
	_, secret, err := registry.Create("remote-bot", grant, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, APIDeps{Access: registry}, ControlDeps{})
	state := &authState{agents: registry}
	state.setHash(mobilebridge.HashPassword("secret12"))
	connects := &connectSink{}
	lan := NewLANManager(router, state, 0, slog.Default(), connects)
	call := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, http.NoBody)
		req.RemoteAddr = "192.168.1.50:5555"
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		lan.dispatch().ServeHTTP(w, req)
		return w
	}
	if w := call(http.MethodGet, "/api/v1/access/whoami", secret); w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"token"`) {
		t.Errorf("whoami over the remote listener: %d %s", w.Code, w.Body)
	}
	if w := call(http.MethodGet, "/api/v1/sessions/x/workspace/file?path=.env", secret); w.Code != 403 {
		t.Errorf("a token off the catalog over the remote listener: status %d, want 403", w.Code)
	}
	if connects.events != 0 {
		t.Errorf("an agent token was reported as a connected device %d time(s)", connects.events)
	}
	if w := call(http.MethodGet, "/api/v1/sessions/x/workspace/file?path=.env", "secret12"); w.Code == 403 || w.Code == 401 {
		t.Errorf("the pairing password must keep its reach: status %d", w.Code)
	}
	if w := call(http.MethodPost, "/api/v1/access/tokens", "secret12"); w.Code != 404 {
		t.Errorf("token management over the remote listener: status %d, want 404", w.Code)
	}
	if _, _, err := registry.Revoke("remote-bot"); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if w := call(http.MethodGet, "/api/v1/access/whoami", secret); w.Code != 401 || !strings.Contains(w.Body.String(), "AGENT_TOKEN_INVALID") {
			t.Fatalf("a revoked token over the remote listener: %d %s", w.Code, w.Body)
		}
	}
	if w := call(http.MethodGet, "/api/v1/access/whoami", "secret12"); w.Code != 200 {
		t.Errorf("an ended token must not lock out its address: status %d", w.Code)
	}
}
