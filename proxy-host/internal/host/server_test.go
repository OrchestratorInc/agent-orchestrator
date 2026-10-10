package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	proxycore "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func boundaryRouter(t *testing.T) (*gin.Engine, *Routes) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	routes := testRoutes(t)
	applyRoutes(t, routes, 1, testRoute("s1", "codex-ticket", "codex", "alice"), testRoute("s2", "claude-ticket", "claude", "bob"))
	b := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(b.Middleware)
	b.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, coreauth.NewManager(nil, nil, nil)), &config.Config{})
	for _, path := range []string{"/v1/responses", "/v1/responses/compact", "/v1/messages", "/v1/messages/count_tokens", "/v1/models"} {
		engine.Any(path, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"auth": c.GetHeader(accountHeader), "provider": c.GetHeader(providerHeader), "authorization": c.GetHeader("Authorization"), "api_key": c.GetHeader("X-Api-Key")})
		})
	}
	engine.Any("/v8/management/*path", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"query": c.Request.URL.RawQuery}) })
	return engine, routes
}
func boundaryRequest(engine *gin.Engine, method, path, token, body string, extra map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range extra {
		req.Header.Set(key, value)
	}
	engine.ServeHTTP(w, req)
	return w
}

func TestBoundaryAcknowledgesAnEmptyRouteListAndItsExactReplay(t *testing.T) {
	engine, routes := boundaryRouter(t)
	for attempt := 0; attempt < 3; attempt++ {
		response := boundaryRequest(engine, "PUT", "/ao/routes", strings.Repeat("c", 32), `{"revision":2,"routes":[]}`, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("empty routing acknowledgement=%d %s", response.Code, response.Body.String())
		}
		var acknowledged Snapshot
		if err := json.Unmarshal(response.Body.Bytes(), &acknowledged); err != nil {
			t.Fatal(err)
		}
		if acknowledged.Revision != 2 || acknowledged.Routes == nil || len(acknowledged.Routes) != 0 {
			t.Fatal("empty routing acknowledgement changed its list representation")
		}
		for _, ticket := range []string{"codex-ticket", "claude-ticket"} {
			if _, done, err := routes.Acquire(ticket); err == nil || done != nil {
				t.Fatal("cleared routing admitted a removed session ticket")
			}
		}
	}
	restored, err := OpenRoutes(routes.path)
	if err != nil || restored.Snapshot().Routes == nil {
		t.Fatalf("empty routing lost its list representation on reopen: %v", err)
	}
}
func TestBoundaryOverwritesAccountAndProviderSpoofs(t *testing.T) {
	engine, _ := boundaryRouter(t)
	for _, tc := range []struct{ path, ticket, account, provider string }{
		{"/v1/responses", "codex-ticket", "alice", "codex"},
		{"/v1/responses/compact", "codex-ticket", "alice", "codex"},
		{"/v1/messages", "claude-ticket", "bob", "claude"},
		{"/v1/messages/count_tokens", "claude-ticket", "bob", "claude"},
		{"/v1/models", "codex-ticket", "alice", "codex"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := boundaryRequest(engine, "POST", tc.path, tc.ticket, "", map[string]string{accountHeader: "spoof", providerHeader: "spoof", "X-Api-Key": "spoof"})
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got["auth"] != tc.account || got["provider"] != tc.provider || got["authorization"] != "Bearer "+strings.Repeat("i", 32) || got["api_key"] != "" {
				t.Fatalf("headers=%v", got)
			}
		})
	}
}
func TestBoundaryClaudeApiKeyTicket(t *testing.T) {
	engine, _ := boundaryRouter(t)
	w := boundaryRequest(engine, "POST", "/v1/messages", "", "", map[string]string{"X-Api-Key": "claude-ticket"})
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestBoundaryRejectsWrongProvider(t *testing.T) {
	engine, _ := boundaryRouter(t)
	for _, tc := range []struct{ path, ticket string }{{"/v1/responses", "claude-ticket"}, {"/v1/messages", "codex-ticket"}, {"/v1/messages/count_tokens", "codex-ticket"}} {
		w := boundaryRequest(engine, "POST", tc.path, tc.ticket, "", nil)
		if w.Code != 400 {
			t.Fatalf("path=%s status=%d", tc.path, w.Code)
		}
	}
}
func TestSessionAndManagementKeysAreSeparated(t *testing.T) {
	engine, _ := boundaryRouter(t)
	for _, token := range []string{"", "unknown", "codex-ticket", strings.Repeat("i", 32)} {
		for _, path := range []string{"/ao/status", "/ao/routes", "/v8/management/credentials"} {
			w := boundaryRequest(engine, "GET", path, token, "", nil)
			if w.Code != 401 {
				t.Fatalf("path=%s token=%q status=%d", path, token, w.Code)
			}
		}
	}
	for _, token := range []string{"", strings.Repeat("c", 32), strings.Repeat("i", 32), "unknown"} {
		w := boundaryRequest(engine, "POST", "/v1/responses", token, "", nil)
		if w.Code != 401 {
			t.Fatalf("inference key=%q status=%d", token, w.Code)
		}
	}
}
func TestBoundaryManagementAllowlist(t *testing.T) {
	engine, _ := boundaryRouter(t)
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "credentials", true}, {"POST", "credentials", true}, {"DELETE", "credentials?name=account.json", true},
		{"GET", "oauth/auth-url?provider=codex", true}, {"GET", "oauth/status?state=s", true},
		{"POST", "oauth/callback", true}, {"DELETE", "oauth/session?state=s", true},
		{"PATCH", "credentials/status", false}, {"GET", "config", false}, {"PUT", "config", false},
		{"GET", "auth-files", false}, {"GET", "api-keys", false}, {"PUT", "routing/strategy", false},
		{"DELETE", "oauth/auth-url", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := boundaryRequest(engine, tc.method, "/v8/management/"+tc.path, strings.Repeat("c", 32), "", nil)
			expected := 404
			if tc.allowed {
				expected = 200
			}
			if w.Code != expected {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBoundaryLegacyQuotaFetchIsPrivateAndOnlyReadOperationAllowed(t *testing.T) {
	engine, _ := boundaryRouter(t)
	engine.Any("/v0/management/*path", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, token := range []string{"", "codex-ticket", "claude-ticket", strings.Repeat("i", 32)} {
		response := boundaryRequest(engine, http.MethodPost, "/v0/management/quota/fetch", token, "{}", nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("non-management token admitted quota fetch: %d", response.Code)
		}
	}
	control := strings.Repeat("c", 32)
	if response := boundaryRequest(engine, http.MethodPost, "/v0/management/quota/fetch", control, "{}", nil); response.Code != http.StatusOK {
		t.Fatalf("private quota fetch status=%d", response.Code)
	}
	for _, path := range []string{"/v0/management/config", "/v0/management/quota/reset", "/v0/management/api-call", "/v8/management/quota/fetch"} {
		if response := boundaryRequest(engine, http.MethodPost, path, control, "{}", nil); response.Code != http.StatusNotFound {
			t.Fatalf("unexpected management path allowed: %s status=%d", path, response.Code)
		}
	}
	if response := boundaryRequest(engine, http.MethodGet, "/v0/management/quota/fetch", control, "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("unexpected quota method allowed: %d", response.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, provider := range []string{"codex", "claude"} {
			path := "/v0/management/" + provider + "-api-key"
			if response := boundaryRequest(engine, method, path, control, "[]", nil); response.Code != http.StatusOK {
				t.Fatalf("native %s %s status=%d", method, path, response.Code)
			}
		}
	}
}

func TestTagAPIKeyAssociatesNativeCredentialWithLogin(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: "key-auth", Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "secret", "base_url": "https://api.example"}}); err != nil {
		t.Fatal(err)
	}
	routes := testRoutes(t)
	engine := gin.New()
	b := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	b.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager), &config.Config{})
	response := boundaryRequest(engine, http.MethodPost, "/ao/tag-api-key", strings.Repeat("c", 32), `{"id":"login-1","provider":"codex","api_key":"secret","base_url":"https://api.example"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	auth, _ := manager.GetByID("key-auth")
	if auth.Metadata["ao_login_id"] != "login-1" {
		t.Fatalf("metadata=%v", auth.Metadata)
	}
	// What the sign-in produced is reported as an API key, not as a sign-in:
	// AO treats the two differently and has nothing else to tell them apart by.
	result := boundaryRequest(engine, http.MethodGet, "/ao/login-result/login-1", strings.Repeat("c", 32), "", nil)
	var verified struct {
		Kind          string `json:"kind"`
		CredentialRef string `json:"credential_ref"`
		AuthID        string `json:"auth_id"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &verified); err != nil || result.Code != http.StatusOK {
		t.Fatalf("status=%d err=%v body=%s", result.Code, err, result.Body.String())
	}
	if verified.Kind != "api_key" || !strings.HasPrefix(verified.CredentialRef, "config-index:codex:") || verified.AuthID != "key-auth" {
		t.Fatalf("verified=%+v", verified)
	}
}

func TestAccountModelsReturnsOnlyTheRequestedCredentialCatalogue(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: "model-auth", Provider: "codex", Status: coreauth.StatusActive}); err != nil {
		t.Fatal(err)
	}
	registry := proxycore.GlobalModelRegistry()
	// Decoded rather than built, because the reasoning metadata type is internal to the SDK.
	var withLevels proxycore.ModelInfo
	if err := json.Unmarshal([]byte(`{"id":"account-gpt","display_name":"Account GPT","type":"codex","thinking":{"levels":["low","medium","high"]}}`), &withLevels); err != nil {
		t.Fatal(err)
	}
	registry.RegisterClient("model-auth", "codex", []*proxycore.ModelInfo{&withLevels, {ID: "account-plain", DisplayName: "Account Plain", Type: "codex"}})
	defer registry.UnregisterClient("model-auth")

	routes := testRoutes(t)
	engine := gin.New()
	b := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	b.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager), &config.Config{})
	response := boundaryRequest(engine, http.MethodPost, "/ao/account-models", strings.Repeat("c", 32), `{"auth_id":"model-auth","provider":"codex"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Models []struct {
			ID      string   `json:"id"`
			Efforts []string `json:"efforts"`
			Default *bool    `json:"is_default"`
		} `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 2 || body.Models[0].ID != "account-gpt" || body.Models[1].ID != "account-plain" {
		t.Fatalf("models=%+v", body.Models)
	}
	// A model's reasoning levels travel with it; a model without any reports none.
	if strings.Join(body.Models[0].Efforts, ",") != "low,medium,high" || len(body.Models[1].Efforts) != 0 {
		t.Fatalf("efforts=%+v", body.Models)
	}
	// Being first in the catalogue does not make a model the default.
	if body.Models[0].Default != nil || body.Models[1].Default != nil {
		t.Fatalf("a model was called the default: %s", response.Body.String())
	}
}

// sessionModelsRouter is a gate in front of a stand-in for CLIProxyAPI's model
// list, which names every account's models and disguises other providers' ones.
func sessionModelsRouter(t *testing.T, status int, answer string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	routes := testRoutes(t)
	applyRoutes(t, routes, 1, testRoute("s1", "codex-ticket", "codex", "alice"), testRoute("s2", "claude-ticket", "claude", "bob"))
	registry := proxycore.GlobalModelRegistry()
	registry.RegisterClient("bob", "claude", []*proxycore.ModelInfo{{ID: "claude-one"}, {ID: "claude-two"}})
	registry.RegisterClient("alice", "codex", []*proxycore.ModelInfo{{ID: "gpt-one"}})
	t.Cleanup(func() { registry.UnregisterClient("bob"); registry.UnregisterClient("alice") })
	b := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(b.Middleware)
	engine.GET("/v1/models", func(c *gin.Context) { c.Data(status, "application/json", []byte(answer)) })
	return engine
}

func TestSessionModelListNamesOnlyTheSessionAccountsModels(t *testing.T) {
	everything := `{"data":[{"type":"model","id":"claude-one","display_name":"One"},{"type":"model","id":"claude-fable-5-dd-eno-tpg","display_name":"Disguised"},{"type":"model","id":"claude-two","display_name":"Two"},{"type":"model","id":"gpt-one"},{"type":"model","id":"claude-other-account"}],"first_id":"claude-one","has_more":false,"last_id":"claude-other-account"}`
	engine := sessionModelsRouter(t, http.StatusOK, everything)
	list := func(ticket string) (ids []string, first, last any) {
		t.Helper()
		response := boundaryRequest(engine, http.MethodGet, "/v1/models?limit=1000", ticket, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			First   any   `json:"first_id"`
			Last    any   `json:"last_id"`
			HasMore *bool `json:"has_more"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.HasMore == nil {
			t.Fatalf("the list lost a field it did not need to change: %s", response.Body.String())
		}
		for _, model := range body.Data {
			ids = append(ids, model.ID)
		}
		return ids, body.First, body.Last
	}

	ids, first, last := list("claude-ticket")
	if strings.Join(ids, ",") != "claude-one,claude-two" || first != "claude-one" || last != "claude-two" {
		t.Fatalf("a Claude session was offered %v (first %v, last %v)", ids, first, last)
	}
	ids, first, last = list("codex-ticket")
	if strings.Join(ids, ",") != "gpt-one" || first != "gpt-one" || last != "gpt-one" {
		t.Fatalf("a Codex session was offered %v (first %v, last %v)", ids, first, last)
	}
}

func TestSessionModelListLeavesOtherAnswersAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		status       int
		answer, path string
	}{
		"Codex's own catalogue": {http.StatusOK, `{"models":[{"slug":"gpt-other-account"}],"data":[{"id":"gpt-other-account"}]}`, "/v1/models?client_version=1.2.3"},
		"an error":              {http.StatusServiceUnavailable, `{"data":[{"id":"gpt-other-account"}]}`, "/v1/models"},
		"not a model list":      {http.StatusOK, `{"object":"list"}`, "/v1/models"},
	} {
		t.Run(name, func(t *testing.T) {
			engine := sessionModelsRouter(t, tc.status, tc.answer)
			response := boundaryRequest(engine, http.MethodGet, tc.path, "codex-ticket", "", nil)
			if response.Code != tc.status || response.Body.String() != tc.answer {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBoundaryDisablesAllInterfaceOAuthForwarder(t *testing.T) {
	engine, _ := boundaryRouter(t)
	w := boundaryRequest(engine, "GET", "/v8/management/oauth/auth-url?provider=codex&is_webui=true", strings.Repeat("c", 32), "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if strings.Contains(w.Body.String(), "is_webui") {
		t.Fatal("upstream all-interface forwarder flag preserved")
	}
	if !strings.Contains(w.Body.String(), "provider=codex") {
		t.Fatal("provider query lost")
	}
}
func TestBoundaryBlocksWebsocketAndUnusedEndpoints(t *testing.T) {
	engine, _ := boundaryRouter(t)
	w := boundaryRequest(engine, "GET", "/v1/responses", "codex-ticket", "", map[string]string{"Upgrade": "websocket"})
	if w.Code != 501 {
		t.Fatalf("websocket=%d", w.Code)
	}
	for _, path := range []string{"/", "/management.html", "/v0/management/auth-files", "/oauth/callback", "/v1/chat/completions", "/redis", "/v1/images/generations"} {
		w = boundaryRequest(engine, "GET", path, "codex-ticket", "", nil)
		if w.Code != 404 {
			t.Fatalf("path=%s status=%d", path, w.Code)
		}
	}
}
func TestPrivateRouteAPIConflictsAndLostAckReplay(t *testing.T) {
	engine, routes := boundaryRouter(t)
	state := routes.Snapshot()
	state.Revision++
	state.Routes[0].AuthID = "replacement"
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := boundaryRequest(engine, "PUT", "/ao/routes", strings.Repeat("c", 32), string(body), nil)
		if w.Code != 200 {
			t.Fatalf("attempt=%d status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	state.Routes[0].AuthID = "other"
	body, _ = json.Marshal(state)
	w := boundaryRequest(engine, "PUT", "/ao/routes", strings.Repeat("c", 32), string(body), nil)
	if w.Code != 409 {
		t.Fatalf("conflict status=%d", w.Code)
	}
	w = boundaryRequest(engine, "PUT", "/ao/routes", strings.Repeat("c", 32), "{invalid", nil)
	if w.Code != 400 {
		t.Fatalf("invalid json status=%d", w.Code)
	}
	got, done, err := routes.Acquire("codex-ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if got.AuthID != "replacement" {
		t.Fatalf("account=%s", got.AuthID)
	}
}
func TestPrivateRouteAPIRunningRequestBlocksUpdate(t *testing.T) {
	engine, routes := boundaryRouter(t)
	_, done, err := routes.Acquire("codex-ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	state := routes.Snapshot()
	state.Revision++
	state.Routes[0].AuthID = "replacement"
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	w := boundaryRequest(engine, "PUT", "/ao/routes", strings.Repeat("c", 32), string(body), nil)
	if w.Code != 409 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestConstantTimeKeyComparisonRequiresNonEmptySecret(t *testing.T) {
	for _, tc := range []struct {
		actual, expected string
		want             bool
	}{
		{"", "", false}, {"Bearer ", "Bearer ", false}, {"Bearer a", "Bearer a", true}, {"Bearer a", "Bearer b", false}, {"Bearer aa", "Bearer a", false},
	} {
		if got := equalKey(tc.actual, tc.expected); got != tc.want {
			t.Fatal(fmt.Sprintf("actual=%q expected=%q got=%t", tc.actual, tc.expected, got))
		}
	}
}
