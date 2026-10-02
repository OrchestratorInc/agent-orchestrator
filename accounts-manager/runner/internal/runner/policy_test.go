package runner

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRunnerPolicyRejectsUnscopedAndUnsupportedOperations(t *testing.T) {
	capability, err := newRouteCapability(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	token := mintTestRoute(t, capability, routeClaims{Provider: "codex", AuthIndex: "selected", SessionID: "session"})
	for _, test := range []struct {
		name, method, path, token string
		ready                     bool
		want                      int
	}{
		{"selected route", "POST", "/v1/responses", token, true, http.StatusNoContent},
		{"missing route", "POST", "/v1/responses", "", true, http.StatusUnauthorized},
		{"internal client key", "POST", "/v1/responses", "client", true, http.StatusUnauthorized},
		{"management is not generation", "POST", "/v1/responses", "management", true, http.StatusUnauthorized},
		{"selector replaced", "POST", "/v1/responses", token, false, http.StatusServiceUnavailable},
		{"unverified websocket", "GET", "/v1/responses", token, true, http.StatusNotImplemented},
		{"unverified execution path", "POST", "/v1/realtime", token, true, http.StatusNotImplemented},
		{"legacy callback", "POST", "/v0/management/oauth-callback", "management", true, http.StatusForbidden},
		{"public callback denied", "POST", "/v0/management/oauth-callback", "", true, http.StatusUnauthorized},
		{"legacy inventory", "GET", "/v0/management/auth-files", "management", true, http.StatusForbidden},
		{"legacy key write", "PUT", "/v0/management/codex-api-key", "management", true, http.StatusForbidden},
		{"legacy import", "POST", "/v0/management/auth-files", "management", true, http.StatusForbidden},
		{"logging mutation", "PUT", "/v0/management/request-log", "management", true, http.StatusForbidden},
		{"selector mutation", "PATCH", "/v0/management/routing/strategy", "management", true, http.StatusForbidden},
		{"config replacement", "PUT", "/v0/management/config.yaml", "management", true, http.StatusForbidden},
		{"direct upstream call", "POST", "/v0/management/api-call", "management", true, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(runnerPolicy("management", capability, func() bool { return test.ready }, func(string, string) bool { return true }))
			router.Handle(test.method, test.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
		})
	}
}

func TestRunnerPolicyRequiresVaultAdmissionForEveryLogicalRoute(t *testing.T) {
	capability, err := newRouteCapability(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	token := mintTestRoute(t, capability, routeClaims{Provider: "codex", AuthIndex: "selected", SessionID: "session"})
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/models"}, {"POST", "/v1/responses"}, {"POST", "/v1/responses/compact"}, {"POST", "/v1/messages"}, {"POST", "/v1/messages/count_tokens"},
	} {
		for _, admitted := range []bool{true, false} {
			router := gin.New()
			checked := false
			router.Use(runnerPolicy("management", capability, func() bool { return true }, func(provider, index string) bool {
				checked = provider == "codex" && index == "selected"
				return admitted
			}))
			router.Handle(route.method, route.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			want := http.StatusNoContent
			if !admitted {
				want = http.StatusUnauthorized
			}
			if !checked || response.Code != want {
				t.Fatalf("%s checked=%v status=%d want=%d", route.path, checked, response.Code, want)
			}
		}
	}
}
