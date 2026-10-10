package host

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/openai"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type authLifecycleHarness struct {
	manager   *coreauth.Manager
	engine    *gin.Engine
	routes    *Routes
	execution *fakeExecutor
}

func newAuthLifecycleHarness(t *testing.T) authLifecycleHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := coreauth.NewManager(nil, exactSelector{}, nil)
	m.SetConfig(&config.Config{})
	m.SetRetryConfig(0, time.Millisecond, 1)
	execution := &fakeExecutor{}
	m.RegisterExecutor(execution)
	for _, id := range []string{"lifecycle-a", "lifecycle-b"} {
		if _, err := m.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: id, Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"email": id + "@example.test", "access_token": "fake-old-token"}}); err != nil {
			t.Fatal(err)
		}
		cliproxy.GlobalModelRegistry().RegisterClient(id, "codex", []*cliproxy.ModelInfo{{ID: "lifecycle-model"}})
		t.Cleanup(func() { cliproxy.GlobalModelRegistry().UnregisterClient(id) })
	}
	routes := testRoutes(t)
	applyRoutes(t, routes, 1, testRoute("session-a", "ticket-a", "codex", "lifecycle-a"), testRoute("session-b", "ticket-b", "codex", "lifecycle-b"))
	base := handlers.NewBaseAPIHandlers(&config.SDKConfig{}, m)
	responses := openai.NewOpenAIResponsesAPIHandler(base)
	engine := gin.New()
	engine.Use(Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}.Middleware)
	engine.POST("/v1/responses", responses.Responses)
	return authLifecycleHarness{manager: m, engine: engine, routes: routes, execution: execution}
}

func (h authLifecycleHarness) request(ticket string, stream bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"lifecycle-model","input":"fake","stream":%t}`, stream)))
	req.Header.Set("Authorization", "Bearer "+ticket)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(accountHeader, "lifecycle-b")
	req.Header.Set(providerHeader, "claude")
	out := httptest.NewRecorder()
	h.engine.ServeHTTP(out, req)
	return out
}

func (h authLifecycleHarness) assertSuccess(t *testing.T, ticket, wanted string, stream bool) {
	t.Helper()
	before := len(h.execution.recorded())
	out := h.request(ticket, stream)
	if out.Code != http.StatusOK {
		t.Fatalf("managed request failed: status=%d body=%s", out.Code, out.Body.String())
	}
	after := h.execution.recorded()
	if len(after) != before+1 || after[before] != wanted {
		t.Fatalf("request changed account selection: calls=%v", after)
	}
	for _, private := range []string{ticket, "fake-old-token", "fake-refreshed-token", "access_token", "refresh_token"} {
		if strings.Contains(out.Body.String(), private) {
			t.Fatal("public inference response leaked a private routing or provider credential")
		}
	}
}

func TestSDKCredentialRefreshKeepsExactAccountAcrossSessionRequests(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream-%t", stream), func(t *testing.T) {
			h := newAuthLifecycleHarness(t)
			before := h.routes.Snapshot()
			h.assertSuccess(t, "ticket-a", "lifecycle-a", stream)
			h.assertSuccess(t, "ticket-b", "lifecycle-b", stream)
			refreshed, err := h.manager.Update(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: "lifecycle-a", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"email": "lifecycle-a@example.test", "access_token": "fake-refreshed-token", "refresh_token": "fake-refresh-token"}})
			if err != nil || refreshed == nil || refreshed.ID != "lifecycle-a" {
				t.Fatalf("SDK credential refresh failed: %v", err)
			}
			h.assertSuccess(t, "ticket-a", "lifecycle-a", stream)
			h.assertSuccess(t, "ticket-b", "lifecycle-b", stream)
			h.assertSuccess(t, "ticket-a", "lifecycle-a", stream)
			if !reflect.DeepEqual(before, h.routes.Snapshot()) {
				t.Fatal("upstream token refresh changed session mappings")
			}
		})
	}
}

func TestSDKAccountDisableAndRemovalNeverSelectAnotherAvailableCredential(t *testing.T) {
	for _, action := range []string{"disable", "disabled-status", "remove"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", action, stream), func(t *testing.T) {
				h := newAuthLifecycleHarness(t)
				h.assertSuccess(t, "ticket-a", "lifecycle-a", stream)
				if action == "remove" {
					h.manager.Remove(coreauth.WithSkipPersist(context.Background()), "lifecycle-a")
				} else {
					entry := &coreauth.Auth{ID: "lifecycle-a", Provider: "codex", Status: coreauth.StatusActive, Disabled: action == "disable"}
					if action == "disabled-status" {
						entry.Status = coreauth.StatusDisabled
					}
					if _, err := h.manager.Update(coreauth.WithSkipPersist(context.Background()), entry); err != nil {
						t.Fatal(err)
					}
				}
				before := append([]string(nil), h.execution.recorded()...)
				for request := 0; request < 3; request++ {
					out := h.request("ticket-a", stream)
					if out.Code == http.StatusOK {
						t.Fatalf("unavailable account request succeeded: %s", out.Body.String())
					}
				}
				if !reflect.DeepEqual(before, h.execution.recorded()) {
					t.Fatal("SDK executed a fallback after account became unavailable")
				}
				h.assertSuccess(t, "ticket-b", "lifecycle-b", stream)
				route, release, err := h.routes.Acquire("ticket-a")
				if err != nil || route.AuthID != "lifecycle-a" {
					t.Fatal("unavailable request rewrote session pin")
				}
				release()
				if action == "remove" {
					if _, err := h.manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: "lifecycle-a", Provider: "codex", Status: coreauth.StatusActive}); err != nil {
						t.Fatal(err)
					}
					cliproxy.GlobalModelRegistry().RegisterClient("lifecycle-a", "codex", []*cliproxy.ModelInfo{{ID: "lifecycle-model"}})
				} else if _, err := h.manager.Update(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: "lifecycle-a", Provider: "codex", Status: coreauth.StatusActive}); err != nil {
					t.Fatal(err)
				}
				h.assertSuccess(t, "ticket-a", "lifecycle-a", stream)
			})
		}
	}
}

func TestSDKRuntimeConfigRefreshRetainsAccountPinAndModelRestrictions(t *testing.T) {
	h := newAuthLifecycleHarness(t)
	h.assertSuccess(t, "ticket-a", "lifecycle-a", false)
	for _, cfg := range []*config.Config{{}, {RequestRetry: 2, MaxRetryCredentials: 10}, {RequestRetry: 0, MaxRetryCredentials: 1}} {
		h.manager.SetConfig(cfg)
		h.assertSuccess(t, "ticket-b", "lifecycle-b", false)
		h.assertSuccess(t, "ticket-a", "lifecycle-a", false)
	}
	cliproxy.GlobalModelRegistry().UnregisterClient("lifecycle-a")
	before := h.execution.recorded()
	out := h.request("ticket-a", false)
	if out.Code == http.StatusOK || !reflect.DeepEqual(before, h.execution.recorded()) {
		t.Fatal("unsupported model caused selection of another account")
	}
	h.assertSuccess(t, "ticket-b", "lifecycle-b", false)
	cliproxy.GlobalModelRegistry().RegisterClient("lifecycle-a", "codex", []*cliproxy.ModelInfo{{ID: "lifecycle-model"}})
	h.assertSuccess(t, "ticket-a", "lifecycle-a", false)
}

func TestSDKReassignmentAfterCredentialRemovalUsesSameNativeTicket(t *testing.T) {
	h := newAuthLifecycleHarness(t)
	h.assertSuccess(t, "ticket-a", "lifecycle-a", false)
	next := h.routes.Snapshot()
	next.Revision++
	next.Routes[0].AuthID = "lifecycle-b"
	if err := h.routes.Apply(next); err != nil {
		t.Fatal(err)
	}
	h.manager.Remove(coreauth.WithSkipPersist(context.Background()), "lifecycle-a")
	h.assertSuccess(t, "ticket-a", "lifecycle-b", false)
	h.assertSuccess(t, "ticket-b", "lifecycle-b", true)
	next = h.routes.Snapshot()
	next.Revision++
	for i := range next.Routes {
		next.Routes[i].AuthID = ""
	}
	if err := h.routes.Apply(next); err != nil {
		t.Fatal(err)
	}
	h.manager.Remove(coreauth.WithSkipPersist(context.Background()), "lifecycle-b")
	before := h.execution.recorded()
	for _, ticket := range []string{"ticket-a", "ticket-b"} {
		out := h.request(ticket, false)
		if out.Code != http.StatusUnauthorized {
			t.Fatalf("waiting session status=%d", out.Code)
		}
	}
	if !reflect.DeepEqual(before, h.execution.recorded()) {
		t.Fatal("waiting sessions reached an executor")
	}
	if _, err := h.manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: "lifecycle-new", Provider: "codex", Status: coreauth.StatusActive}); err != nil {
		t.Fatal(err)
	}
	cliproxy.GlobalModelRegistry().RegisterClient("lifecycle-new", "codex", []*cliproxy.ModelInfo{{ID: "lifecycle-model"}})
	t.Cleanup(func() { cliproxy.GlobalModelRegistry().UnregisterClient("lifecycle-new") })
	next = h.routes.Snapshot()
	next.Revision++
	for i := range next.Routes {
		next.Routes[i].AuthID = "lifecycle-new"
	}
	if err := h.routes.Apply(next); err != nil {
		t.Fatal(err)
	}
	h.assertSuccess(t, "ticket-a", "lifecycle-new", true)
	h.assertSuccess(t, "ticket-b", "lifecycle-new", false)
}
