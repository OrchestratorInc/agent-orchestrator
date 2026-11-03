package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

// fakeJCodeOllama answers `model list --json` with jcode's mixed list and
// `auth status --json` with the given Ollama mode.
func fakeJCodeOllama(t *testing.T, method string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	list := write("list.json", `{"provider":"Ollama","selected_model":"qwen3:1.7b","models":["gpt-5.6-pro[web]","anthropic/claude-opus-5.5","qwen3:1.7b","llama3.2","claude-opus-5-5","gpt-5.6"],"routes":[]}`)
	status := write("status.json", `{"any_available":true,"providers":[{"id":"ollama","status":"available","method":"`+method+`"}]}`)
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *'model list --json'*) cat '" + list + "' ;;\n" +
		"  *'auth status --json'*) cat '" + status + "' ;;\n" +
		"  *) exit 4 ;;\nesac\n"
	binary := filepath.Join(dir, "jcode")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil { //nolint:gosec // executable fixture
		t.Fatal(err)
	}
	return binary
}

func jcodeModelIDs(catalog ports.AgentModelCatalog) string {
	ids := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
	}
	return strings.Join(ids, ",")
}

// In jcode's keyless local Ollama mode the catalog is what the Ollama server
// has installed, not jcode's built-in hosted model names.
func TestJCodeCatalogListsOnlyInstalledModelsForLocalOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:1.7b","model":"qwen3:1.7b"},{"name":"llama3.2:latest","model":"llama3.2:latest"}]}`))
	}))
	defer server.Close()
	binary := fakeJCodeOllama(t, "local endpoint (`"+server.URL+"/v1`)")
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "jcode", Binary: binary, WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got := jcodeModelIDs(catalog); got != "qwen3:1.7b,llama3.2" {
		t.Fatalf("models = %s, want only the installed Ollama models", got)
	}
	if !catalog.Models[0].IsDefault {
		t.Fatalf("selected model is not the default: %+v", catalog.Models)
	}

	// A local server that does not answer is an error, not jcode's built-in list.
	server.Close()
	if _, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "jcode", Binary: binary, WorkingDir: t.TempDir()}); err == nil {
		t.Fatal("Discover succeeded with the local Ollama server down")
	}
}

// With a stored (possibly cloud) Ollama key jcode is not in its keyless local
// mode, so its own list stands and no local server is asked.
func TestJCodeCatalogKeepsTheNativeListOutsideKeylessOllama(t *testing.T) {
	binary := fakeJCodeOllama(t, "local endpoint (`http://127.0.0.1:1/v1`) + optional API key (`OLLAMA_API_KEY`)")
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "jcode", Binary: binary, WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got := jcodeModelIDs(catalog); !strings.Contains(got, "claude-opus-5-5") || !strings.Contains(got, "qwen3:1.7b") {
		t.Fatalf("models = %s, want jcode's own list", got)
	}
}
