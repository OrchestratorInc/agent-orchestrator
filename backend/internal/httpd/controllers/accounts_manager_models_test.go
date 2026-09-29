package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
)

func TestAccountModelHTTPPreservesEffortsAndRedactsPrivateMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			if r.URL.Query().Get("ref") != "private-ref" {
				t.Error("model lookup lost exact account")
			}
			_, _ = w.Write([]byte(`{"models":[{"id":"managed-model","display_name":"Managed model","thinking":{"levels":["low","high"],"private":"private-capability"},"token":"private-token"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"files":[{"auth_index":"private-ref","provider":"codex","status":"active","verification":"verified"}]}`))
	}))
	defer server.Close()
	client := core.NewManagementClient(maintenanceEndpoint{server.URL}, server.Client())
	id, err := client.CredentialPublicID("private-ref")
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	controller := AccountsManagerController{Service: accountsvc.New(client)}
	controller.Register(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/accounts-manager/accounts/"+id+"/models", nil))
	var body AccountsManagerModelsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK || len(body.Models) != 1 || len(body.Models[0].Efforts) != 2 || body.Models[0].Efforts[1] != "high" {
		t.Fatalf("scoped model capabilities missing: status=%d body=%s error=%v", response.Code, response.Body.String(), err)
	}
	for _, private := range []string{"private-ref", "private-capability", "private-token", "test-management"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("private model metadata escaped")
		}
	}
}
