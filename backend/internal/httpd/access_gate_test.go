package httpd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/access"
	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type gateFixture struct {
	t               *testing.T
	dataDir         string
	registry        *access.Registry
	router          chi.Router
	served          []string
	bodies          []string
	spawnID         string
	returnsExisting bool
	beforeCreate    func()
	afterCreate     func()
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	f := &gateFixture{t: t, dataDir: t.TempDir(), spawnID: "api-9"}
	registry, err := access.LoadRegistry(access.TokensPath(f.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	f.registry = registry
	r := chi.NewRouter()
	gate := &accessGate{
		registry: registry, routes: r, now: time.Now,
		sessionProject: func(_ context.Context, sessionID string) (string, bool) {
			project, ok := map[string]string{"api-1": "api", "api-9": "api", "web-1": "web"}[sessionID]
			return project, ok
		},
	}
	r.Use(caller.Middleware)
	r.Use(gate.middleware)
	ok := func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		f.served = append(f.served, req.Method+" "+req.URL.Path)
		f.bodies = append(f.bodies, string(body))
		w.WriteHeader(http.StatusOK)
	}
	r.Get("/healthz", ok)
	r.Get("/api/v1/sessions", ok)
	r.Post("/api/v1/sessions", func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		f.bodies = append(f.bodies, string(body))
		if f.beforeCreate != nil {
			f.beforeCreate()
		}
		if !f.returnsExisting {
			if err := f.registry.SessionCreated(req.Context(), f.spawnID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if f.afterCreate != nil {
			f.afterCreate()
		}
		_, _ = io.WriteString(w, `{"session":{"id":"`+f.spawnID+`"}}`)
	})
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
	r.Post("/api/v1/sessions/{sessionId}/conversation/messages", ok)
	r.Patch("/api/v1/sessions/{sessionId}/merge-policy", ok)
	r.Get("/api/v1/sessions/{sessionId}/workspace/tree", ok)
	r.Post("/api/v1/browser/commands", ok)
	f.router = r
	return f
}

func (f *gateFixture) token(name, preset string, projects ...string) string {
	f.t.Helper()
	grant, err := access.NewGrant(preset, nil, projects, time.Time{})
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
	method, path, body, token, session string
}

func (f *gateFixture) do(call gateCall) (status int, code, reason string) {
	f.t.Helper()
	req := httptest.NewRequest(call.method, call.path, strings.NewReader(call.body))
	if call.token != "" {
		req.Header.Set("Authorization", "Bearer "+call.token)
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
	} {
		if status, _, _ := f.do(call); status != http.StatusOK {
			t.Errorf("%s %s: status %d, want 200", call.method, call.path, status)
		}
	}
}

func TestGateHoldsATokenToItsGrant(t *testing.T) {
	f := newGateFixture(t)
	read := f.token("reader", access.PresetRead)
	operateAPI := f.token("api-bot", access.PresetOperate, "api")
	manage := f.token("admin", access.PresetManage)
	spawn := func(project string) string { return `{"projectId":"` + project + `","prompt":"hi"}` }

	for name, tc := range map[string]struct {
		call   gateCall
		status int
		code   string
		reason string
	}{
		"read lists sessions":             {gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: read}, 200, "", ""},
		"read cannot send":                {gateCall{method: http.MethodPost, path: "/api/v1/sessions/api-1/send", token: read}, 403, "GRANT_REFUSED", access.ReasonGroup},
		"probe is open":                   {gateCall{method: http.MethodGet, path: "/healthz", token: read}, 200, "", ""},
		"whoami is open":                  {gateCall{method: http.MethodGet, path: "/api/v1/access/whoami", token: read}, 200, "", ""},
		"route outside the catalog":       {gateCall{method: http.MethodGet, path: "/api/v1/mobile/status", token: manage}, 403, "GRANT_REFUSED", access.ReasonCatalog},
		"token cannot make tokens":        {gateCall{method: http.MethodPost, path: "/api/v1/access/tokens", token: manage}, 403, "GRANT_REFUSED", access.ReasonCatalog},
		"unknown route":                   {gateCall{method: http.MethodGet, path: "/api/v1/nope", token: manage}, 404, "ROUTE_NOT_FOUND", ""},
		"unknown token":                   {gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: access.TokenPrefix + "ff"}, 401, "AGENT_TOKEN_INVALID", ""},
		"limited: session in its project": {gateCall{method: http.MethodPost, path: "/api/v1/sessions/api-1/send", token: operateAPI}, 200, "", ""},
		"limited: session elsewhere":      {gateCall{method: http.MethodPost, path: "/api/v1/sessions/web-1/send", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: unknown session":        {gateCall{method: http.MethodGet, path: "/api/v1/sessions/ghost", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: list needs a project":   {gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: list in its project":    {gateCall{method: http.MethodGet, path: "/api/v1/sessions?project=api", token: operateAPI}, 200, "", ""},
		"limited: every project":          {gateCall{method: http.MethodGet, path: "/api/v1/projects", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: machine data":           {gateCall{method: http.MethodGet, path: "/api/v1/settings", token: operateAPI}, 200, "", ""},
		"limited: spawn in its project":   {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("api"), token: operateAPI}, 200, "", ""},
		"limited: spawn elsewhere":        {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("web"), token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: spawn with no project":  {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"prompt":"hi"}`, token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"manage removes a project":        {gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", token: manage}, 200, "", ""},
		"limited: second spelling of key": {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api","ProjectId":"web"}`, token: operateAPI}, 400, "GRANT_TARGET_UNREADABLE", ""},
		"limited: other spelling of key":  {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"PROJECTID":"api"}`, token: operateAPI}, 400, "GRANT_TARGET_UNREADABLE", ""},
		"limited: first of two values":    {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("api") + spawn("web"), token: operateAPI}, 200, "", ""},
		"limited: first value elsewhere":  {gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: spawn("web") + spawn("api"), token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
		"limited: cues of its project":    {gateCall{method: http.MethodGet, path: "/api/v1/projects/api/cues", token: operateAPI}, 200, "", ""},
		"limited: escaped slash in an id": {gateCall{method: http.MethodGet, path: "/api/v1/projects/api%2Fcues", token: operateAPI}, 403, "GRANT_REFUSED", access.ReasonProject},
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

func TestSessionStartedUnderAGrantKeepsIt(t *testing.T) {
	f := newGateFixture(t)
	operateAPI := f.token("api-bot", access.PresetOperate, "api")
	if status, _, _ := f.do(gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api"}`, token: operateAPI}); status != 200 {
		t.Fatalf("spawn: status %d", status)
	}
	if _, bound := f.registry.ForSession("api-9"); !bound {
		t.Fatal("the started session is not bound to the token")
	}
	f.spawnID, f.returnsExisting = "api-1", true
	if status, _, _ := f.do(gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api"}`, token: operateAPI}); status != 200 {
		t.Fatalf("a spawn that returns an existing session: status %d", status)
	}
	if _, bound := f.registry.ForSession("api-1"); bound {
		t.Error("a session that existed before the call must not be bound to the caller's token")
	}
	f.spawnID, f.returnsExisting = "api-9", false
	asSession := func(method, path string) (int, string, string) {
		return f.do(gateCall{method: method, path: path, session: "api-9"})
	}
	if status, _, _ := asSession(http.MethodGet, "/api/v1/sessions?project=api"); status != 200 {
		t.Errorf("the session may still read its project: status %d", status)
	}
	if status, _, reason := asSession(http.MethodDelete, "/api/v1/projects/api"); status != 403 || reason != access.ReasonGroup {
		t.Errorf("the session must not exceed the grant: %d %q", status, reason)
	}
	if status, _, reason := asSession(http.MethodPost, "/api/v1/access/tokens"); status != 403 || reason != access.ReasonCatalog {
		t.Errorf("the session must not make tokens: %d %q", status, reason)
	}
	ownTooling := func(when string) {
		if status, _, _ := asSession(http.MethodPost, "/api/v1/sessions/api-9/activity"); status != 200 {
			t.Errorf("%s: the session's status hook: status %d, want 200", when, status)
		}
		if status, _, _ := asSession(http.MethodGet, "/api/v1/sessions/api-9/workspace/tree"); status != 200 {
			t.Errorf("%s: a read of the session's own files: status %d, want 200", when, status)
		}
	}
	ownTooling("under the grant")
	if status, _, _ := asSession(http.MethodPost, "/api/v1/browser/commands"); status != 200 {
		t.Errorf("the session's browser tool under the grant: status %d, want 200", status)
	}
	for _, route := range [][2]string{{http.MethodPost, "/api/v1/sessions/api-9/conversation/messages"}, {http.MethodPatch, "/api/v1/sessions/api-9/merge-policy"}} {
		if status, _, reason := asSession(route[0], route[1]); status != 403 || reason != access.ReasonCatalog {
			t.Errorf("%s %s is neither a catalog action nor the session's own tooling: %d %q", route[0], route[1], status, reason)
		}
	}
	if status, _, reason := asSession(http.MethodDelete, "/api/v1/sessions/api-9"); status != 403 || reason != access.ReasonGroup {
		t.Errorf("the session must not run an action on itself that its grant lacks: %d %q", status, reason)
	}
	if status, _, _ := asSession(http.MethodPost, "/api/v1/sessions/api-9/send"); status != 200 {
		t.Errorf("an action on itself that the grant allows: status %d, want 200", status)
	}
	if status, _, reason := asSession(http.MethodPost, "/api/v1/sessions/api-1/activity"); status != 403 || reason != access.ReasonCatalog {
		t.Errorf("another session's own route: %d %q", status, reason)
	}
	if status, _, reason := asSession(http.MethodPost, "/api/v1/sessions/api-9/fork"); status != 403 || reason != access.ReasonCatalog {
		t.Errorf("a fork would be a new session with no grant: %d %q", status, reason)
	}
	if status, _, reason := f.do(gateCall{method: http.MethodPost, path: "/api/v1/sessions/api-9/activity", token: operateAPI}); status != 403 || reason != access.ReasonCatalog {
		t.Errorf("a token has no own session: %d %q", status, reason)
	}
	if status, _, _ := f.do(gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api"}`, session: "api-9"}); status != 200 {
		t.Errorf("the session may start a session in its project: status %d", status)
	}
	everyProject := f.token("free-bot", access.PresetOperate)
	for _, body := range []string{`{"projectId":"api","kind":"orchestrator"}`, `{"projectId":"api","Kind":"orchestrator"}`, `{"projectId":"api","kind":"orchestrator"} trailing`} {
		for _, token := range []string{operateAPI, everyProject} {
			if status, _, _ := f.do(gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: body, token: token}); status != 403 && status != 400 {
				t.Errorf("a token must not start a project's orchestrator with %s: status %d", body, status)
			}
		}
	}

	if _, found, err := f.registry.Revoke("api-bot"); err != nil || !found {
		t.Fatalf("revoke: %v %v", found, err)
	}
	if status, code, _ := f.do(gateCall{method: http.MethodGet, path: "/api/v1/sessions", token: operateAPI}); status != 401 || code != "AGENT_TOKEN_INVALID" {
		t.Errorf("a revoked token: %d %q", status, code)
	}
	if status, _, reason := asSession(http.MethodGet, "/api/v1/sessions?project=api"); status != 403 || reason != access.ReasonEnded {
		t.Errorf("a session of a revoked token: %d %q", status, reason)
	}
	ownTooling("after the grant ended")
	if status, _, reason := asSession(http.MethodPost, "/api/v1/browser/commands"); status != 403 || reason != access.ReasonEnded {
		t.Errorf("the browser tool after the grant ended: %d %q", status, reason)
	}
	if status, _, reason := asSession(http.MethodPost, "/api/v1/sessions/api-9/send"); status != 403 || reason != access.ReasonEnded {
		t.Errorf("an action on itself after the grant ended: %d %q", status, reason)
	}
	if status, _, _ := asSession(http.MethodGet, "/api/v1/sessions/api-9"); status != 200 {
		t.Errorf("the session may still read itself after the grant ended: status %d", status)
	}
}

func TestSpawnNeverLeavesASessionWithoutItsGrant(t *testing.T) {
	f := newGateFixture(t)
	operateAPI := f.token("api-bot", access.PresetOperate, "api")
	spawn := gateCall{method: http.MethodPost, path: "/api/v1/sessions", body: `{"projectId":"api"}`, token: operateAPI}

	f.spawnID = "api-6"
	f.afterCreate = func() {
		status, _, reason := f.do(gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", session: "api-6"})
		if status != 403 || reason != access.ReasonGroup {
			t.Errorf("the new session calling the daemon while its own spawn still runs: %d %q, want 403 %q", status, reason, access.ReasonGroup)
		}
	}
	if status, _, _ := f.do(spawn); status != 200 {
		t.Fatalf("spawn: status %d", status)
	}
	f.afterCreate = nil

	f.spawnID = "api-7"
	f.beforeCreate = func() {
		if _, found, err := f.registry.Revoke("api-bot"); err != nil || !found {
			t.Fatalf("revoke during the spawn: %v %v", found, err)
		}
	}
	if status, _, _ := f.do(spawn); status != 200 {
		t.Fatalf("spawn: status %d", status)
	}
	if status, _, reason := f.do(gateCall{method: http.MethodGet, path: "/api/v1/sessions?project=api", session: "api-7"}); status != 403 || reason != access.ReasonEnded {
		t.Errorf("a session whose token was revoked during its spawn: %d %q, want 403 %q", status, reason, access.ReasonEnded)
	}

	f.beforeCreate = nil
	f.spawnID = "api-8"
	operateAgain := f.token("api-bot-2", access.PresetOperate, "api")
	spawn.token = operateAgain
	bindings := filepath.Join(f.dataDir, "access", "sessions")
	if err := os.RemoveAll(bindings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bindings, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := f.do(spawn); status != 500 {
		t.Errorf("a spawn whose binding cannot be written must fail: status %d, want 500", status)
	}
	if status, _, reason := f.do(gateCall{method: http.MethodDelete, path: "/api/v1/projects/api", session: "api-8"}); status != 403 || reason != access.ReasonGroup {
		t.Errorf("the session must stay limited for this run: %d %q", status, reason)
	}
}

func TestRealRouterGatesTokensAndServesWhoami(t *testing.T) {
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := access.NewGrant(access.PresetRead, nil, []string{"api"}, time.Time{})
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
			Preset   string
			Projects []string
		}
	}
	if w := call(http.MethodGet, "/api/v1/access/whoami", secret); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &who) != nil ||
		who.Kind != access.CallerToken || who.Name != "reader" || who.Grant == nil || who.Grant.Preset != access.PresetRead || len(who.Grant.Projects) != 1 {
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
		if want := slices.Contains(grant.Groups, entry.Group) && entry.Project != access.ProjectAll; action.Allowed != want {
			t.Errorf("%s: allowed = %v for a read grant limited to one project, want %v", action.ID, action.Allowed, want)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	short, _ := access.NewGrant(access.PresetRead, nil, nil, past.Add(time.Hour))
	expired, _, err := registry.Create("expired", short, past)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Bind("api-1", expired.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/access/actions", http.NoBody)
	req.Header.Set(caller.Header, "api-1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &actions) != nil {
		t.Fatalf("actions for a session: %d %s", w.Code, w.Body)
	}
	for _, action := range actions.Actions {
		if action.Allowed {
			t.Errorf("%s reads as allowed for a session whose token has expired", action.ID)
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

func TestOwnSessionRoutesExistInTheAPISpec(t *testing.T) {
	for _, routes := range []map[string]bool{ownSessionRoutes, sessionToolRoutes, grantOpenRoutes} {
		for route := range routes {
			method, path, _ := strings.Cut(route, " ")
			if strings.HasPrefix(path, "/api/v1/") && apispec.Default().Operation(method, path) == nil {
				t.Errorf("%s is open to a caller with a grant, but the API spec has no such route", route)
			}
		}
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
	grant, _ := access.NewGrant(access.PresetRead, nil, nil, time.Time{})
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
