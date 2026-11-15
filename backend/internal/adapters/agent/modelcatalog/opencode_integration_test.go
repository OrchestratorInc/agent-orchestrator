package modelcatalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func resolveRealOpenCode(t *testing.T) string {
	t.Helper()
	if path, err := exec.LookPath("opencode"); err == nil && path != "" {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".opencode", "bin", "opencode")
		if info, err := os.Stat(candidate); err == nil && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	t.Skip("opencode binary not installed; skipping live discovery integration test")
	return ""
}

func TestOpenCodeDiscoveryRunsAgainstRealBinary(t *testing.T) {
	binary := resolveRealOpenCode(t)
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "opencode",
		Binary:  binary,
	})
	if err != nil {
		t.Fatalf("live opencode discovery failed: %v", err)
	}
	if len(catalog.Models) == 0 {
		t.Fatalf("live opencode discovery returned no models")
	}
}

func TestOpenCodeCredentialScopeListsProviderModels(t *testing.T) {
	binary := resolveRealOpenCode(t)
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID:        "opencode",
		Binary:         binary,
		CredentialType: "anthropic_api_key",
	})
	if err != nil {
		t.Fatalf("credential-scoped opencode discovery failed: %v", err)
	}
	var anthropic int
	for _, model := range catalog.Models {
		if strings.HasPrefix(model.ID, "anthropic/") {
			anthropic++
		}
	}
	if anthropic == 0 {
		t.Fatalf("credential scope anthropic_api_key listed no anthropic/* models (got %d models); "+
			"provider-presence env did not unlock the provider", len(catalog.Models))
	}
}
