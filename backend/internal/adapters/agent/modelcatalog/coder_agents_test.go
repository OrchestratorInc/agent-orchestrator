package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Coder Agents' catalog is the deployment's enabled model configs. The ID AO
// stores is the model config UUID Coder's chat API takes, and disabled
// models are not offered.
func TestCoderAgentsCatalogComesFromDeployment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Coder-Session-Token") != "token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v2/users/me/organizations":
			_, _ = w.Write([]byte(`[{"id":"o1","name":"coder","is_default":true}]`))
		case "/api/v2/organizations/o1/chats/models":
			_, _ = w.Write([]byte(`{"models":[` +
				`{"id":"m1","model":"glm-5","display_name":"GLM 5","enabled":true,"is_default":true,"context_limit":128000,"reasoning_efforts":["low","high"]},` +
				`{"id":"m2","model":"glm-4.6","enabled":true},` +
				`{"id":"m3","model":"disabled","enabled":false}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	catalog, err := Discoverer{}.Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "coder-agents", Binary: "/test/bin/coder",
		Env: map[string]string{"CODER_URL": server.URL, "CODER_SESSION_TOKEN": "token"},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if catalog.SelectionMode != ports.ModelSelectionCatalog || catalog.AllowCustom || len(catalog.Models) != 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
	first := catalog.Models[0]
	if first.ID != "m1" || first.Label != "GLM 5" || !first.IsDefault || first.ContextWindow != 128000 || len(first.Efforts) != 2 {
		t.Fatalf("first model = %+v", first)
	}
	if catalog.Models[1].Label != "glm-4.6" {
		t.Fatalf("unnamed model label = %q", catalog.Models[1].Label)
	}
}

func TestCoderAgentsCatalogFingerprintOmitsToken(t *testing.T) {
	env := map[string]string{"CODER_URL": "https://coder.example.com", "CODER_SESSION_TOKEN": "secret-token"}
	fingerprint := discoveryConfigInputs(context.Background(), "coder-agents", "", env)
	if strings.Contains(fingerprint, "secret-token") || !strings.Contains(fingerprint, "coder.example.com") {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
}
