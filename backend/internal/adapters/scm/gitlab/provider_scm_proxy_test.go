package gitlab

import (
	"net/http"
	"testing"
)

// The SCM wiring injects one shared *http.Client (SCM proxy support) through
// ProviderOptions. The per-host clients built lazily for self-managed hosts
// must carry the same injected client — otherwise a configured proxy would
// silently apply to gitlab.com only.
func TestNewProvider_InjectedHTTPClientReachesHostClients(t *testing.T) {
	injected := &http.Client{}
	p, err := NewProvider(ProviderOptions{
		HTTPClient:   injected,
		AllowedHosts: []string{"gitlab.internal.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := p.clientForHost("gitlab.internal.example")
	if c == nil {
		t.Fatal("clientForHost returned nil for an allowed host")
	}
	if c.http != injected {
		t.Fatalf("per-host client transport source = %p, want the injected client %p", c.http, injected)
	}
}
