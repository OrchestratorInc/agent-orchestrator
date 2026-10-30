package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func fakeJCodeModels(t *testing.T, body string, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Without JCODE_NON_INTERACTIVE the real CLI opens a login picker instead.
	script := "#!/bin/sh\n[ \"$JCODE_NON_INTERACTIVE\" = 1 ] || { echo 'Choose a provider:'; exit 9; }\n" +
		"case \"$*\" in *'model list --json'*) cat '" + out + "'; exit " + string(rune('0'+exit)) + ";; esac\nexit 4\n"
	binary := filepath.Join(dir, "jcode")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil { //nolint:gosec // executable fixture
		t.Fatal(err)
	}
	return binary
}

func TestJCodeCatalogListsNativeModelsWithSelectedDefault(t *testing.T) {
	binary := fakeJCodeModels(t, `{"provider":"Claude","selected_model":"claude-opus-5-5","models":["claude-fable-5-1","claude-opus-5-5","claude-sonnet-5"],"routes":[]}`, 0)
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "jcode", Binary: binary, WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.SelectionMode != ports.ModelSelectionCatalog || catalog.CustomModelEntry != ports.CustomModelEntryDirect || catalog.Source != "cli" {
		t.Fatalf("catalog shape = %+v", catalog)
	}
	ids := []string{}
	defaults := 0
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
		if model.IsDefault {
			defaults++
			if model.ID != "claude-opus-5-5" {
				t.Fatalf("default = %q", model.ID)
			}
		}
	}
	if strings.Join(ids, ",") != "claude-opus-5-5,claude-fable-5-1,claude-sonnet-5" || defaults != 1 {
		t.Fatalf("models = %v (defaults %d)", ids, defaults)
	}
}

func TestJCodeCatalogReportsSignInWithoutOpeningThePicker(t *testing.T) {
	binary := fakeJCodeModels(t, "Error: No credentials configured. Run 'jcode login' or set ANTHROPIC_API_KEY to authenticate.\n", 1)
	_, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "jcode", Binary: binary, WorkingDir: t.TempDir()})
	if !errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired) {
		t.Fatalf("err = %v, want sign-in required", err)
	}
}
