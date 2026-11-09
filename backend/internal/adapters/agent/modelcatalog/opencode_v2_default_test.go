package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestParseOpenCodeV2DefaultModel(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
	}{
		{"provider and model", `{"location":{"directory":"/x"},"data":{"id":"iceman-free","modelID":"iceman-free","providerID":"opencode"}}`, "opencode/iceman-free"},
		{"model id already provider-qualified", `{"data":{"modelID":"opencode/iceman-free","providerID":"opencode"}}`, "opencode/iceman-free"},
		{"no default model", `{"location":{"directory":"/x"},"data":null}`, ""},
		{"missing data", `{"location":{"directory":"/x"}}`, ""},
		{"blank model", `{"data":{"modelID":"  ","providerID":"opencode"}}`, ""},
		{"log noise before the JSON", "update available\n{\"data\":{\"modelID\":\"m\",\"providerID\":\"p\"}}\n", "p/m"},
		{"not JSON", "boom", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseOpenCodeV2DefaultModel([]byte(tc.output)); got != tc.want {
				t.Fatalf("parseOpenCodeV2DefaultModel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeOpenCodeV2 writes an opencode stand-in that lists two models and answers
// `api GET /api/model/default` with apiBody (or fails when apiFails). Every
// invocation's argv is appended to the returned log file.
func fakeOpenCodeV2(t *testing.T, apiBody string, apiFails bool) (binary, argvLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake binary")
	}
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv.log")
	apiBranch := `printf '%s' '` + apiBody + `'`
	if apiFails {
		apiBranch = `echo "server failed" >&2; exit 1`
	}
	script := "#!/bin/sh\necho \"$*\" >> '" + argvLog + "'\n" +
		"case \"$1\" in\n" +
		"  models) printf 'opencode-go/beam-free\\nopencode/iceman-free\\n' ;;\n" +
		"  api) " + apiBranch + " ;;\n" +
		"esac\n"
	binary = filepath.Join(dir, "opencode")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, argvLog
}

func discoverOpenCodeV2(t *testing.T, binary string) ports.AgentModelCatalog {
	t.Helper()
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID:    "opencode-v2",
		Binary:     binary,
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return catalog
}

func defaultModels(models []ports.AgentModelInfo) []string {
	var out []string
	for _, m := range models {
		if m.IsDefault {
			out = append(out, m.ID)
		}
	}
	return out
}

// OpenCode 2 reads config AO cannot see (ancestors of the launch worktree), so
// AO's own file reading never resolves a default for it. Ask the CLI instead:
// without this the picker fell back to the first listed model, which says
// nothing about what opencode will run.
func TestOpenCodeV2DiscoveryMarksTheModelOpenCodeReports(t *testing.T) {
	binary, argvLog := fakeOpenCodeV2(t, `{"location":{"directory":"x"},"data":{"modelID":"iceman-free","providerID":"opencode"}}`, false)
	catalog := discoverOpenCodeV2(t, binary)

	if got := defaultModels(catalog.Models); len(got) != 1 || got[0] != "opencode/iceman-free" {
		t.Fatalf("default models = %v, want [opencode/iceman-free] (beam-free is only first alphabetically)", got)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "api --standalone GET /api/model/default") {
		t.Fatalf("opencode was not asked for its default model; argv log:\n%s", raw)
	}
}

func TestOpenCodeV2DiscoveryKeepsCatalogWhenDefaultLookupFails(t *testing.T) {
	binary, _ := fakeOpenCodeV2(t, "", true)
	catalog := discoverOpenCodeV2(t, binary)

	if len(catalog.Models) != 2 {
		t.Fatalf("models = %d, want the two listed models despite the failed lookup", len(catalog.Models))
	}
	if got := defaultModels(catalog.Models); len(got) != 0 {
		t.Fatalf("default models = %v, want none: an unknown default must not be guessed", got)
	}
}

func TestOpenCodeV2DiscoveryIgnoresANullDefault(t *testing.T) {
	binary, _ := fakeOpenCodeV2(t, `{"location":{"directory":"x"},"data":null}`, false)
	catalog := discoverOpenCodeV2(t, binary)
	if got := defaultModels(catalog.Models); len(got) != 0 {
		t.Fatalf("default models = %v, want none", got)
	}
}

// Other agents must not be asked: only opencode-v2 has this endpoint.
func TestOpenCodeV1DiscoveryDoesNotQueryTheV2DefaultEndpoint(t *testing.T) {
	binary, argvLog := fakeOpenCodeV2(t, `{"data":{"modelID":"iceman-free","providerID":"opencode"}}`, false)
	if _, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "opencode", Binary: binary, WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	raw, _ := os.ReadFile(argvLog)
	if strings.Contains(string(raw), "api ") {
		t.Fatalf("opencode (v1) must not call the v2 API; argv log:\n%s", raw)
	}
}

// A catalog cached before OpenCode 2 was asked for its default has none, so the
// fingerprint must differ from the old one or the stale snapshot is reused.
func TestOpenCodeV2CatalogFingerprintInvalidatesSnapshotsWithoutADefault(t *testing.T) {
	request := ports.AgentModelDiscoveryRequest{AgentID: "opencode-v2", Binary: "/nonexistent/opencode"}
	v2 := (Discoverer{}).CatalogFingerprint(context.Background(), request)
	if !strings.HasSuffix(v2, ":resolved-default-v1") {
		t.Fatalf("opencode-v2 fingerprint = %q, want the resolved-default suffix", v2)
	}
	request.AgentID = "opencode"
	if v1 := (Discoverer{}).CatalogFingerprint(context.Background(), request); strings.Contains(v1, "resolved-default") {
		t.Fatalf("opencode (v1) fingerprint = %q must be unchanged", v1)
	}
}
