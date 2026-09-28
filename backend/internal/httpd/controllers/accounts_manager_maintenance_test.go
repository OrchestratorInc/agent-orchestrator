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

type maintenanceEndpoint struct{ url string }

func (s maintenanceEndpoint) Endpoint() (core.Endpoint, bool) {
	return core.Endpoint{BaseURL: s.url, ManagementToken: "test-management"}, true
}

func TestAccountsManagerVerificationErrorsAreSafeAndDistinct(t *testing.T) {
	for _, test := range []struct {
		privateStatus, publicStatus int
		code                        string
	}{
		{422, 400, "ACCOUNTS_MANAGER_INVALID_CREDENTIAL"},
		{424, 503, "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE"},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ao/internal/credentials/api-key" {
					t.Error("unexpected verification call")
				}
				w.WriteHeader(test.privateStatus)
				_, _ = w.Write([]byte(`{"message":"private-token http://127.0.0.1:9999/internal"}`))
			}))
			defer server.Close()
			controller := AccountsManagerController{Service: accountsvc.New(core.NewManagementClient(maintenanceEndpoint{server.URL}, server.Client()))}
			router := chi.NewRouter()
			router.Use(middleware.RequestID)
			controller.Register(router)
			request := httptest.NewRequest(http.MethodPost, "/accounts-manager/accounts/api-key", strings.NewReader(`{"provider":"codex","key":"synthetic-private-key"}`))
			request.Header.Set("X-Request-ID", "verification-79")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var body struct {
				Code      string
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != test.publicStatus || body.Code != test.code || body.RequestID != "verification-79" {
				t.Fatalf("verification result=%d %+v", response.Code, body)
			}
			for _, secret := range []string{"private-token", "synthetic-private-key", "127.0.0.1", "test-management"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatal("private verification data escaped")
				}
			}
		})
	}
}

func TestAccountsManagerMaintenanceCommands(t *testing.T) {
	var operationIDs []string
	var labels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer test-management" {
			t.Error("missing private authentication")
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"auth_index": "private-ref", "provider": "codex", "account_type": "oauth", "generation": 2, "label": "Work", "status": "active"}}})
		case http.MethodPost:
			var input struct {
				OperationID string `json:"operationId"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				t.Error("invalid create payload")
			}
			operationIDs = append(operationIDs, input.OperationID)
			_ = json.NewEncoder(w).Encode(map[string]any{"auth_index": "private-ref", "provider": "codex", "account_type": "oauth"})
		case http.MethodPatch:
			if r.URL.Path != "/ao/internal/credentials/label" || r.URL.Query().Get("ref") != "private-ref" {
				t.Error("incorrect rename target")
			}
			var input struct {
				Label      string
				Generation uint64
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Generation != 2 {
				t.Error("rename lost generation")
			}
			labels = append(labels, input.Label)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected private command")
		}
	}))
	defer server.Close()
	client := core.NewManagementClient(maintenanceEndpoint{server.URL}, server.Client())
	controller := AccountsManagerController{Service: accountsvc.New(client)}
	router := chi.NewRouter()
	controller.Register(router)
	id, err := client.CredentialPublicID("private-ref")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api-key", `{"operationId":"key-retry","provider":"codex","key":"synthetic-private-key"}`, 201},
		{"POST", "/api-key", `{"operationId":"key-retry","provider":"codex","key":"synthetic-private-key"}`, 201},
		{"POST", "/import", `{"operationId":"import-retry","provider":"codex","filename":"fixture.json","credential":{"type":"codex","access_token":"synthetic-private-key"}}`, 201},
		{"PATCH", "/" + id, `{"label":"Production","generation":2}`, 200},
		{"PATCH", "/" + id, `{}`, 400},
		{"PATCH", "/" + id, `{"label":"wrong","disabled":true,"generation":2}`, 400},
		{"PATCH", "/" + id, `{"label":"stale"}`, 400},
	} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(test.method, "/accounts-manager/accounts"+test.path, strings.NewReader(test.body)))
		if res.Code != test.status {
			t.Fatalf("%s status=%d want=%d", test.path, res.Code, test.status)
		}
		for _, secret := range []string{"private-ref", "synthetic-private-key", "test-management"} {
			if strings.Contains(res.Body.String(), secret) {
				t.Fatal("private data crossed public projection")
			}
		}
	}
	if len(operationIDs) != 3 || operationIDs[0] != "key-retry" || operationIDs[1] != "key-retry" || operationIDs[2] != "import-retry" || len(labels) != 1 || labels[0] != "Production" {
		t.Fatal("mutation scope or idempotency lost")
	}
}
