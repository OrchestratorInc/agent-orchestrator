package modelcatalog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func codexEndpointRequest(baseURL, key, model string) ports.AgentModelDiscoveryRequest {
	return ports.AgentModelDiscoveryRequest{AgentID: "codex", Env: map[string]string{
		harnessendpoint.CodexBaseURLEnv: baseURL,
		harnessendpoint.CodexAPIKeyEnv:  key,
		harnessendpoint.CodexModelEnv:   model,
	}}
}

// A Codex session on a custom endpoint runs the endpoint's models, so the
// picker lists those instead of Codex's OpenAI catalog.
func TestCodexEndpointListsTheEndpointModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"MiniMax-M2"},{"id":"MiniMax-M2.5"}]}`)
	}))
	defer server.Close()
	appServerAsked := false
	d := Discoverer{CodexModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
		appServerAsked = true
		return []ports.ChatModel{{ID: "gpt-5.5", Default: true}}, nil
	}}

	catalog, err := d.Discover(context.Background(), codexEndpointRequest(server.URL+"/v1", "sk-test", "MiniMax-M2.5"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if appServerAsked {
		t.Fatal("Codex's own catalog must not be asked for a custom endpoint")
	}
	if len(catalog.Models) != 2 || catalog.Models[0].ID != "MiniMax-M2" || catalog.Models[1].ID != "MiniMax-M2.5" {
		t.Fatalf("models = %+v, want the endpoint's models in its order", catalog.Models)
	}
	if catalog.Models[0].IsDefault || !catalog.Models[1].IsDefault {
		t.Fatalf("models = %+v, want the saved endpoint model as the default", catalog.Models)
	}
}

// A refused key or an endpoint without a listing fails discovery, so the
// service keeps the cached catalog or offers typed model IDs, never the
// OpenAI catalog. The error never carries the key.
func TestCodexEndpointDiscoveryFailsWithoutAListing(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		d := Discoverer{CodexModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
			t.Error("Codex's own catalog must not be asked for a custom endpoint")
			return nil, nil
		}}
		catalog, err := d.Discover(context.Background(), codexEndpointRequest(server.URL+"/v1", "sk-secret", ""))
		server.Close()
		if err == nil || strings.Contains(err.Error(), "sk-secret") || len(catalog.Models) != 0 {
			t.Fatalf("status %d: catalog = %+v, err = %v; want a key-free failure and no models", status, catalog.Models, err)
		}
	}
}

func TestCodexEndpointFingerprintFollowsTheEndpoint(t *testing.T) {
	if got := codexEndpointFingerprint(map[string]string{}); got != "" {
		t.Fatalf("no endpoint: fingerprint = %q, want empty so cached catalogs stay valid", got)
	}
	a := codexEndpointFingerprint(codexEndpointRequest("https://a.example.com/v1", "k1", "m").Env)
	for name, env := range map[string]map[string]string{
		"url":   codexEndpointRequest("https://b.example.com/v1", "k1", "m").Env,
		"key":   codexEndpointRequest("https://a.example.com/v1", "k2", "m").Env,
		"model": codexEndpointRequest("https://a.example.com/v1", "k1", "n").Env,
	} {
		if codexEndpointFingerprint(env) == a {
			t.Fatalf("changing the %s must change the fingerprint", name)
		}
	}
	if strings.Contains(a, "k1") {
		t.Fatalf("fingerprint %q must not carry the key", a)
	}
}
