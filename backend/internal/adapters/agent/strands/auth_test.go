package strands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeAuthProbeUsesReadOnlyNativeDiscovery(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for _, tc := range []struct {
		name, provider, result string
		want                   ports.AgentAuthStatus
	}{
		{"verified", "openai", `return {available:true,models:[{id:"fixture"}]}`, ports.AgentAuthStatusAuthorized},
		{"empty", "openai", `return {available:true,models:[]}`, ports.AgentAuthStatusConfigured},
		{"unavailable", "openai", `return {available:false,models:[],error:"rate limited"}`, ports.AgentAuthStatusConfigured},
		{"rejected", "openai", `return {available:false,models:[],credentialRejected:true}`, ports.AgentAuthStatusUnauthorized},
		{"discovery error", "openai", `throw Error("fixture-secret-must-not-escape")`, ports.AgentAuthStatusConfigured},
		{"custom endpoint", "openai", `throw Error("must not probe a different endpoint")`, ports.AgentAuthStatusConfigured},
		{"unsupported", "litellm", `throw Error("must not probe")`, ports.AgentAuthStatusConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := setup(t, `{"profile":{"model":"`+tc.provider+`/fixture"}}`)
			t.Setenv("OPENAI_API_KEY", "fixture-secret-must-not-escape")
			if tc.name == "custom endpoint" {
				t.Setenv("OPENAI_BASE_URL", "https://invalid.example/v1")
			}
			t.Setenv("LITELLM_API_KEY", "fixture-secret-must-not-escape")
			root := t.TempDir()
			bin := filepath.Join(root, "bin", "strands.js")
			mustWriteAuthFixture(t, bin, "")
			mustWriteAuthFixture(t, filepath.Join(root, "package.json"), `{"name":"@strands-agents/cli","version":"0.2.0","type":"module"}`)
			mustWriteAuthFixture(t, filepath.Join(root, "dist", "src", "tui", "config.js"), `export class CliConfigStore { static memory(permissions,settings,setup) { if (!setup || typeof setup.profile.model !== "string") throw Error("invalid native setup arguments"); const stored={...setup.providerEnvironment}; for(const key of ["OPENAI_API_KEY","ANTHROPIC_API_KEY","GEMINI_API_KEY","LITELLM_API_KEY"]) delete stored[key]; return {providerEnvironment() { const records=Object.fromEntries(Object.entries(stored).map(([key,value])=>[key,{value,source:"config"}])); for(const [key,value] of Object.entries(process.env)) if(value) records[key]={value,source:"process"}; return records; }} } }`)
			mustWriteAuthFixture(t, filepath.Join(root, "dist", "src", "tui", "provider", "discovery.js"), `export async function discoverProviderModels() {`+tc.result+`}`)
			config := filepath.Join(os.Getenv("HOME"), ".strands", "cli", "config.json")
			before, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			status, err := probeNativeAuthentication(context.Background(), bin)
			if err != nil || status != tc.want {
				t.Fatalf("status=%s error=%v want=%s", status, err, tc.want)
			}
			after, err := os.ReadFile(config)
			if err != nil || string(after) != string(before) {
				t.Fatal("auth probe changed native config")
			}
			// The public auth boundary must use the probe instead of promoting key presence.
			p.resolvedBinary = bin
			status, err = p.AuthStatus(context.Background())
			if err != nil || status != tc.want {
				t.Fatalf("AuthStatus=%s error=%v want=%s", status, err, tc.want)
			}
		})
	}
}

func TestNativeAuthProbeRejectsCustomConfigurationAndWrongPackage(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for _, cfg := range []string{`{"agentProject":"./secret-project"}`, `{"profile":{"model":"openai/fixture","modelModule":{"module":"./secret-module"}}}`} {
		_, _ = setup(t, cfg)
		root := t.TempDir()
		bin := filepath.Join(root, "bin", "strands.js")
		mustWriteAuthFixture(t, bin, "")
		mustWriteAuthFixture(t, filepath.Join(root, "package.json"), `{"name":"@strands-agents/cli","version":"0.2.0","type":"module"}`)
		mustWriteAuthFixture(t, filepath.Join(root, "dist", "src", "tui", "config.js"), `throw Error("must not import custom config")`)
		status, err := probeNativeAuthentication(context.Background(), bin)
		if err != nil || status != ports.AgentAuthStatusUnknown {
			t.Fatalf("custom config status=%s error=%v", status, err)
		}
	}
	_, _ = setup(t, `{"profile":{"model":"openai/fixture"}}`)
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "strands.js")
	mustWriteAuthFixture(t, bin, "")
	mustWriteAuthFixture(t, filepath.Join(root, "package.json"), `{"name":"other-cli","version":"0.2.0","type":"module"}`)
	if _, err := probeNativeAuthentication(context.Background(), bin); err == nil {
		t.Fatal("untrusted package accepted")
	}
	mustWriteAuthFixture(t, filepath.Join(root, "package.json"), `{"name":"@strands-agents/cli","version":"0.2.2","type":"module"}`)
	if _, err := probeNativeAuthentication(context.Background(), bin); err == nil {
		t.Fatal("unqualified version accepted")
	}
}

func TestNativeAuthProbeOutputRejectsAnythingExceptStatus(t *testing.T) {
	for _, raw := range []string{`{"status":"authorized","secret":"must-not-escape"}`, `{"status":"made-up"}`, `{"status":"authorized"} trailing`} {
		if _, err := nativeAuthenticationStatus([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	raw, _ := json.Marshal(map[string]string{"status": "authorized"})
	if status, err := nativeAuthenticationStatus(raw); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("%s %v", status, err)
	}
}

func mustWriteAuthFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAuthProbeActualPackageEnvironmentWithoutNetwork(t *testing.T) {
	modules := os.Getenv("STRANDS_NODE_MODULES")
	if modules == "" {
		t.Skip("set STRANDS_NODE_MODULES for the offline native API smoke")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	root := filepath.Join(modules, "@strands-agents", "cli")
	bin := filepath.Join(root, "bin", "strands.js")
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "LITELLM_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "OPENAI_BASE_URL", "ANTHROPIC_BASE_URL"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		name                              string
		configured, invalidConfigEndpoint bool
	}{
		{"legacy-config-key-only", false, false},
		{"process-key-native-discovery", true, false},
		{"unsupported-config-endpoint", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configJSON := `{"profile":{"model":"openai/fixture"},"providers":{"environment":{"OPENAI_API_KEY":"ignored-legacy-fixture","AWS_REGION":"us-east-1"}}}`
			if tc.invalidConfigEndpoint {
				configJSON = `{"profile":{"model":"openai/fixture"},"providers":{"environment":{"OPENAI_BASE_URL":"https://invalid.example/v1"}}}`
			}
			_, _ = setup(t, configJSON)
			if tc.configured {
				t.Setenv("OPENAI_API_KEY", "synthetic-offline-fixture")
			}
			config := filepath.Join(os.Getenv("HOME"), ".strands", "cli", "config.json")
			before, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			// fetch is replaced before native discovery is imported. Count discovery's
			// attempt, then reject BEFORE any socket/request can be created.
			expectedFetches := 0
			if tc.configured && !tc.invalidConfigEndpoint {
				expectedFetches = 1
			}
			script := `let deniedFetches=0;
globalThis.fetch=async()=>{deniedFetches++;throw Error("network disabled by offline fixture");};
` + nativeAuthenticationProbe + fmt.Sprintf(`
if(deniedFetches!==%d) throw Error("native discovery reachability mismatch");
`, expectedFetches)
			cmd := exec.Command(node, "--input-type=module", "--eval", script, bin)
			out, err := cmd.Output()
			want := `{"status":"unknown"}`
			if expectedFetches == 1 {
				want = `{"status":"configured"}`
			}
			if err != nil || string(out) != want {
				t.Fatalf("native smoke failed: error=%v valid-status=%t", err, string(out) == want)
			}
			after, err := os.ReadFile(config)
			if err != nil || string(after) != string(before) {
				t.Fatal("native probe wrote config")
			}
		})
	}
}
