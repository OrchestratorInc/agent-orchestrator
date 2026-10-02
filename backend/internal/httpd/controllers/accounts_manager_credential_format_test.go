package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
)

func TestCredentialFormatHTTPRejectsWrongMethodBeforeServiceMutation(t *testing.T) {
	var calls atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth_index":"private-ref","provider":"codex","account_type":"api_key","files":[]}`))
	}))
	defer private.Close()
	service := accountsvc.New(core.NewManagementClient(maintenanceEndpoint{private.URL}, private.Client()))
	before := service.Snapshot().Revision
	controller := AccountsManagerController{Service: service}
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	controller.Register(router)
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/accounts-manager/accounts/api-key", strings.NewReader(`{"operationId":"wrong-method","provider":"codex","key":"sk-ant-oat01-synthetic-only"}`))
		request.Header.Set("X-Request-ID", "credential-format-79")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var result struct {
			Code      string `json:"code"`
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || response.Code != http.StatusBadRequest || result.Code != "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED" || result.RequestID != "credential-format-79" {
			t.Errorf("wrong-method response status=%d code=%q requestId=%q", response.Code, result.Code, result.RequestID)
		}
		for _, privateValue := range []string{"synthetic-only", "private-ref", private.URL, "test-management"} {
			if strings.Contains(response.Body.String(), privateValue) {
				t.Error("wrong-method response contains private data")
			}
		}
	}
	if calls.Load() != 0 || service.Snapshot().Revision != before {
		t.Errorf("wrong-method request changed service state: calls=%d", calls.Load())
	}
}

func TestCredentialFormatHTTPProjectsLegacyKindWithoutSecret(t *testing.T) {
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/ao/internal/credentials" {
			t.Error("inventory projection attempted a mutation")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"auth_index":"private-ref","provider":"codex","account_type":"access_token","generation":4,"verification":"verified","verified_at":"2026-09-29T00:00:00Z","status":"active","supports_quota":true,"api_key":"synthetic-only"}]}`))
	}))
	defer private.Close()
	controller := AccountsManagerController{Service: accountsvc.New(core.NewManagementClient(maintenanceEndpoint{private.URL}, private.Client()))}
	router := chi.NewRouter()
	controller.Register(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/accounts-manager/accounts", nil))
	var result AccountsManagerAccountsResponse
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || response.Code != http.StatusOK || len(result.Accounts) != 1 {
		t.Fatalf("legacy inventory status=%d count=%d", response.Code, len(result.Accounts))
	}
	account := result.Accounts[0]
	if account.Kind != "access_token" || account.QuotaSupported || account.Generation != 4 || account.Disabled || account.Verification != "verified" {
		t.Error("legacy inventory changed identity/proof or invented quota support")
	}
	for _, privateValue := range []string{"synthetic-only", "private-ref", private.URL, "test-management"} {
		if strings.Contains(response.Body.String(), privateValue) {
			t.Error("private credential material crossed inventory projection")
		}
	}
}
