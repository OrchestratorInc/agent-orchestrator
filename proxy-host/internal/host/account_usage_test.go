package host

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// usageExecutor scripts the provider's answers to the usage request and
// CLIProxy's token refresh.
type usageExecutor struct {
	fakeExecutor
	statuses     []int
	refreshErr   error
	httpCalls    int
	refreshCalls int
}

func (*usageExecutor) PrepareRequest(*http.Request, *coreauth.Auth) error { return nil }
func (e *usageExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	status := e.statuses[min(e.httpCalls, len(e.statuses)-1)]
	e.httpCalls++
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"plan_type":"pro"}`))}, nil
}
func (e *usageExecutor) Refresh(_ context.Context, a *coreauth.Auth) (*coreauth.Auth, error) {
	e.refreshCalls++
	if e.refreshErr != nil {
		return nil, e.refreshErr
	}
	return a, nil
}

func usageRouter(t *testing.T, e *usageExecutor, attributes map[string]string) (*gin.Engine, *coreauth.Manager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := coreauth.NewManager(nil, exactSelector{}, nil)
	m.SetConfig(&config.Config{})
	m.RegisterExecutor(e)
	if _, err := m.Register(context.Background(), &coreauth.Auth{ID: "account", Provider: "codex", Status: coreauth.StatusActive, Attributes: attributes}); err != nil {
		t.Fatal(err)
	}
	b := Boundary{Routes: testRoutes(t), ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(b.Middleware)
	b.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, m), &config.Config{})
	return engine, m
}

func TestAccountUsageConfirmsRejectedSignInThroughTokenRefresh(t *testing.T) {
	request := func(engine *gin.Engine) int {
		return boundaryRequest(engine, "POST", "/ao/account-usage", strings.Repeat("c", 32), `{"auth_id":"account","provider":"codex"}`, nil).Code
	}
	t.Run("healthy account is not refreshed", func(t *testing.T) {
		e := &usageExecutor{statuses: []int{200}}
		engine, _ := usageRouter(t, e, nil)
		if code := request(engine); code != 200 || e.httpCalls != 1 || e.refreshCalls != 0 {
			t.Fatalf("code=%d http=%d refresh=%d", code, e.httpCalls, e.refreshCalls)
		}
	})
	t.Run("stale token is refreshed and usage retried", func(t *testing.T) {
		e := &usageExecutor{statuses: []int{401, 200}}
		engine, m := usageRouter(t, e, nil)
		if code := request(engine); code != 200 || e.httpCalls != 2 || e.refreshCalls != 1 {
			t.Fatalf("code=%d http=%d refresh=%d", code, e.httpCalls, e.refreshCalls)
		}
		if a, _ := m.GetByID("account"); a == nil || a.Status != coreauth.StatusActive {
			t.Fatalf("recovered account was left unhealthy: %+v", a)
		}
	})
	t.Run("dead sign-in is recorded on the account by CLIProxy", func(t *testing.T) {
		e := &usageExecutor{statuses: []int{401}, refreshErr: &coreauth.Error{Code: "unauthorized", Message: "unauthorized", HTTPStatus: 401}}
		engine, m := usageRouter(t, e, nil)
		if code := request(engine); code != http.StatusBadGateway || e.httpCalls != 1 || e.refreshCalls != 1 {
			t.Fatalf("code=%d http=%d refresh=%d", code, e.httpCalls, e.refreshCalls)
		}
		if a, _ := m.GetByID("account"); a == nil || a.Status == coreauth.StatusActive {
			t.Fatalf("failed refresh left the account marked healthy: %+v", a)
		}
	})
	t.Run("API-key account is never refreshed", func(t *testing.T) {
		e := &usageExecutor{statuses: []int{401}}
		engine, _ := usageRouter(t, e, map[string]string{"api_key": "sk-test"})
		if code := request(engine); code != http.StatusBadGateway || e.httpCalls != 1 || e.refreshCalls != 0 {
			t.Fatalf("code=%d http=%d refresh=%d", code, e.httpCalls, e.refreshCalls)
		}
	})
}
