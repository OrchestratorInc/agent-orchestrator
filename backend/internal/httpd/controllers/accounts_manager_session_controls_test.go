package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
)

func TestAccountsManagerSessionControlsUnavailableBoundary(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	controller := AccountsManagerController{
		Status: fakeAccountsManagerStatus{status: accountsmanager.Status{State: accountsmanager.StateReady}},
	}
	router.Route("/api/v1", controller.Register)

	t.Run("catalog readiness does not imply control availability", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/accounts-manager/status", nil))
		var status AccountsManagerStatusResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || status.State != "ready" {
			t.Fatalf("status control = %d %s", response.Code, response.Body.String())
		}
	})

	for _, test := range []struct {
		name, method, path, body string
	}{
		{"binding", http.MethodGet, "/sessions/session-a/account", ""},
		{"start switch", http.MethodPost, "/sessions/session-a/account-switches", `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"interrupt","newConversation":false}`},
		{"switch status", http.MethodGet, "/sessions/session-a/account-switches/switch-a", ""},
		{"retry switch", http.MethodPost, "/sessions/session-a/account-switches/switch-a/retry", "{}"},
		{"cancel switch", http.MethodPost, "/sessions/session-a/account-switches/switch-a/cancel", "{}"},
		{"removal impact", http.MethodGet, "/accounts-manager/accounts/amc_a/removal-impact", ""},
		{"start removal", http.MethodPost, "/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":7,"confirmed":true}`},
		{"removal status", http.MethodGet, "/accounts-manager/removals/removal-a", ""},
		{"retry removal", http.MethodPost, "/accounts-manager/removals/removal-a/retry", "{}"},
		{"cancel removal", http.MethodPost, "/accounts-manager/removals/removal-a/cancel", "{}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/v1"+test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(middleware.RequestIDHeader, "public-control-unavailable")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotImplemented {
				t.Fatalf("missing public control boundary: %s %s = %d, want %d; body=%q", test.method, test.path, response.Code, http.StatusNotImplemented, response.Body.String())
			}
			var body struct {
				Code      string `json:"code"`
				RequestID string `json:"requestId"`
				Message   string `json:"message"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("unavailable control must use the public error envelope: %v", err)
			}
			if body.Code != "NOT_IMPLEMENTED" || body.RequestID != "public-control-unavailable" || body.Message == "" {
				t.Fatalf("lost unavailable state or request identity: %+v", body)
			}
		})
	}
}
