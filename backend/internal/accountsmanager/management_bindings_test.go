package accountsmanager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSynchronizeBindingsUsesPrivateOrderedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/ao/internal/routes/bindings" || r.Header.Get("Authorization") != "Bearer management-key" {
			t.Error("incorrect binding transport")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var snapshot BindingSnapshot
		if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if snapshot.Revision != 7 || len(snapshot.Bindings) != 2 || snapshot.Bindings[0].Mode != "native" || snapshot.Bindings[1].AccountID != "public-id" || snapshot.Bindings[1].Revision != 7 {
			t.Error("binding snapshot lost durable intent")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "management-key"}, ready: true}, server.Client())
	if err := client.SynchronizeBindings(t.Context(), BindingSnapshot{Revision: 7, Bindings: []RouteBinding{
		{SessionID: "native-session", Provider: "codex", Mode: "native", Revision: 1},
		{SessionID: "managed-session", Provider: "codex", Mode: "managed", AccountID: "public-id", Revision: 7},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := client.SynchronizeBindings(t.Context(), BindingSnapshot{}); err == nil {
		t.Fatal("missing durable clock accepted")
	}
}
