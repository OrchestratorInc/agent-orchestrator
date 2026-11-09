package modelcatalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// resolveRealOpenCode finds an opencode binary the way the daemon would, so the
// integration tests run the user's actual CLI. It skips (never fails) when no
// opencode is installed, keeping CI green on machines without it.
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

// TestOpenCodeDiscoveryRunsAgainstRealBinary is the regression guard for the
// "Unrecognized flag: --pure" failure: it runs the exact command the daemon
// builds against the installed opencode and asserts a non-empty catalog.
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

// TestOpenCodeCredentialScopeListsProviderModels proves the cloud path
// end-to-end: a credential type marks that provider's env var present, so the
// real `opencode models` lists that provider's catalog (what the cloud VM runs).
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

// TestOpenCodeV2DiscoveryReportsTheRealDefault runs discovery against the
// installed OpenCode 2 and checks that whatever default it marks is one the
// catalog lists, and that at most one is marked. It skips when no opencode 2 is
// installed, and never fails on a machine where opencode has no usable default.
func TestOpenCodeV2DiscoveryReportsTheRealDefault(t *testing.T) {
	binary := resolveRealOpenCode(t)
	version, err := exec.Command(binary, "--version").Output()
	if err != nil || !regexp.MustCompile(`(^|\s)v?2\.`).Match(version) {
		t.Skipf("not OpenCode 2 (%q)", strings.TrimSpace(string(version)))
	}
	workingDir := t.TempDir()
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID:    "opencode-v2",
		Binary:     binary,
		WorkingDir: workingDir,
	})
	if err != nil {
		t.Fatalf("live opencode-v2 discovery failed: %v", err)
	}
	var defaults []string
	for _, m := range catalog.Models {
		if m.IsDefault {
			defaults = append(defaults, m.ID)
		}
	}
	if len(defaults) > 1 {
		t.Fatalf("more than one default marked: %v", defaults)
	}
	t.Logf("OpenCode %s reported default: %v (of %d models)", strings.TrimSpace(string(version)), defaults, len(catalog.Models))
}
