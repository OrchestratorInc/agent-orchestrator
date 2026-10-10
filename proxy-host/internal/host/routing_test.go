package host

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/openai"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type fakeExecutor struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (*fakeExecutor) Identifier() string { return "codex" }
func (e *fakeExecutor) Execute(_ context.Context, a *coreauth.Auth, _ executor.Request, _ executor.Options) (executor.Response, error) {
	e.mu.Lock()
	e.calls = append(e.calls, a.ID)
	e.mu.Unlock()
	if e.fail {
		return executor.Response{}, &coreauth.Error{Code: "quota", Message: "fake quota exhausted", HTTPStatus: 429}
	}
	return executor.Response{Payload: []byte(fmt.Sprintf(`{"id":%q,"object":"response","output":[]}`, a.ID))}, nil
}
func (e *fakeExecutor) ExecuteStream(_ context.Context, a *coreauth.Auth, _ executor.Request, _ executor.Options) (*executor.StreamResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, a.ID)
	e.mu.Unlock()
	if e.fail {
		return nil, &coreauth.Error{Code: "quota", Message: "fake quota exhausted", HTTPStatus: 429}
	}
	ch := make(chan executor.StreamChunk, 1)
	ch <- executor.StreamChunk{Payload: []byte(fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"object\":\"response\",\"output\":[]}}\n\n", a.ID))}
	close(ch)
	return &executor.StreamResult{Chunks: ch}, nil
}
func (*fakeExecutor) Refresh(_ context.Context, a *coreauth.Auth) (*coreauth.Auth, error) {
	return a, nil
}
func (*fakeExecutor) CountTokens(context.Context, *coreauth.Auth, executor.Request, executor.Options) (executor.Response, error) {
	return executor.Response{}, nil
}
func (*fakeExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("unused")
}

func setupRouting(t *testing.T, disabledA, fail bool) (*gin.Engine, *fakeExecutor) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := &fakeExecutor{fail: fail}
	m := coreauth.NewManager(nil, exactSelector{}, nil)
	m.SetConfig(&config.Config{})
	m.SetRetryConfig(2, time.Millisecond, 2)
	m.RegisterExecutor(e)
	for _, id := range []string{"account-a", "account-b"} {
		a := &coreauth.Auth{ID: id, Provider: "codex", Status: coreauth.StatusActive, Disabled: disabledA && id == "account-a"}
		if _, err := m.Register(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		cliproxy.GlobalModelRegistry().RegisterClient(id, "codex", []*cliproxy.ModelInfo{{ID: "probe-model"}})
		t.Cleanup(func() { cliproxy.GlobalModelRegistry().UnregisterClient(id) })
	}
	h := openai.NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&config.SDKConfig{}, m))
	r := gin.New()
	routes := testRoutes(t)
	applyRoutes(t, routes, 1,
		testRoute("s-a", "session-a", "codex", "account-a"),
		testRoute("s-b", "session-b", "codex", "account-b"),
		testRoute("s-missing", "session-missing", "codex", "missing-account"))
	r.Use(Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}.Middleware)
	r.POST("/v1/responses", h.Responses)
	return r, e
}

func sendProbe(r *gin.Engine, token string, stream bool) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"probe-model","input":"fake","stream":%t}`, stream)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-AO-Auth-ID", "account-b") // Spoofed selection must be replaced.
	r.ServeHTTP(w, req)
	return w
}

func TestPublicHTTPAndSSEExactSelection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream-%t", stream), func(t *testing.T) {
			r, e := setupRouting(t, false, false)
			for _, token := range []string{"session-a", "session-b", "session-a"} {
				w := sendProbe(r, token, stream)
				if w.Code != 200 {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			}
			if got := strings.Join(e.recorded(), ","); got != "account-a,account-b,account-a" {
				t.Fatalf("calls=%s", got)
			}
		})
	}
}

func TestUnavailableSelectionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, token    string
		disabled, fail bool
	}{
		{"unknown-token", "unknown", false, false},
		{"missing-account", "session-missing", false, false},
		{"disabled-account", "session-a", true, false},
		{"quota-and-retry", "session-a", false, true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-stream-%t", tc.name, stream), func(t *testing.T) {
				r, e := setupRouting(t, tc.disabled, tc.fail)
				w := sendProbe(r, tc.token, stream)
				if w.Code == 200 {
					t.Fatalf("unexpected success: %s", w.Body.String())
				}
				for _, id := range e.recorded() {
					if id != "account-a" {
						t.Fatalf("unexpected fallback: %v", e.recorded())
					}
				}
				if !tc.fail && len(e.recorded()) != 0 {
					t.Fatalf("executed unavailable account: %v", e.recorded())
				}
				if tc.fail && len(e.recorded()) == 0 {
					t.Fatal("quota fixture never executed")
				}
			})
		}
	}
}

func (e *fakeExecutor) recorded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}
