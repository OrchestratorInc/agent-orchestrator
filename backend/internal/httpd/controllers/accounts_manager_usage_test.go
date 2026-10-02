package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
)

func TestAccountsManagerUsageErrorsPreserveCategoryAndRequestID(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{
		{401, "ACCOUNTS_MANAGER_USAGE_AUTHENTICATION_REQUIRED"},
		{403, "ACCOUNTS_MANAGER_USAGE_ACCESS_DENIED"},
		{429, "ACCOUNTS_MANAGER_USAGE_RATE_LIMITED"},
		{503, "ACCOUNTS_MANAGER_USAGE_UNAVAILABLE"},
		{502, "ACCOUNTS_MANAGER_USAGE_RESPONSE_INVALID"},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("usage attempted to mutate account")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/ao/internal/credentials":
					_, _ = w.Write([]byte(`{"files":[{"auth_index":"private-ref","provider":"codex","account_type":"oauth","supports_quota":true}]}`))
				case "/ao/internal/credentials/quota":
					w.WriteHeader(test.status)
					_, _ = w.Write([]byte(`{"message":"private-token http://127.0.0.1:9999/internal"}`))
				default:
					t.Error("unexpected usage request")
				}
			}))
			defer server.Close()
			client := core.NewManagementClient(maintenanceEndpoint{server.URL}, server.Client())
			service := accountsvc.New(client)
			if _, err := service.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			id, err := client.CredentialPublicID("private-ref")
			if err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Use(middleware.RequestID)
			(&AccountsManagerController{Service: service}).Register(router)
			request := httptest.NewRequest(http.MethodGet, "/accounts-manager/accounts/"+id+"/quota", nil)
			request.Header.Set("X-Request-ID", "usage-check-79")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var body struct {
				Code      string
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != test.status || body.Code != test.code || body.RequestID != "usage-check-79" {
				t.Fatalf("usage error = %d %+v, want %d %s", response.Code, body, test.status, test.code)
			}
			for _, secret := range []string{"private-ref", "private-token", "127.0.0.1", "test-management"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatal("usage error exposed private material")
				}
			}
		})
	}
}
