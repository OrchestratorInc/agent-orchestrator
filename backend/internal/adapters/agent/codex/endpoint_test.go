package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func endpointTestEnv() map[string]string {
	return map[string]string{
		harnessendpoint.CodexBaseURLEnv: "https://gateway.example.com/v1",
		harnessendpoint.CodexAPIKeyEnv:  "sk-endpoint-secret",
		harnessendpoint.CodexModelEnv:   "gpt-oss-120b",
	}
}

func TestEndpointArgsDefineProviderWithoutTheKey(t *testing.T) {
	args := EndpointArgs(endpointTestEnv())
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"model_providers.ao_endpoint.base_url='https://gateway.example.com/v1'",
		"model_providers.ao_endpoint.env_key='" + harnessendpoint.CodexAPIKeyEnv + "'",
		"model_providers.ao_endpoint.wire_api='responses'",
		"model_provider='ao_endpoint'",
		"model='gpt-oss-120b'",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("EndpointArgs missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "sk-endpoint-secret") {
		t.Fatalf("the API key must stay in env, never in argv: %v", args)
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-c" {
			t.Fatalf("args must be -c pairs: %v", args)
		}
	}
}

func TestEndpointArgsNeedURLAndKey(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"none":      nil,
		"no key":    {harnessendpoint.CodexBaseURLEnv: "https://gateway.example.com"},
		"no url":    {harnessendpoint.CodexAPIKeyEnv: "k"},
		"blank url": {harnessendpoint.CodexBaseURLEnv: " ", harnessendpoint.CodexAPIKeyEnv: "k"},
	} {
		if args := EndpointArgs(env); args != nil {
			t.Fatalf("%s: EndpointArgs = %v, want nil", name, args)
		}
	}
}

func TestLaunchAndRestoreCommandsCarryEndpointFromEnv(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:     "session-1",
		WorkspacePath: canonicalTempDir(t),
		Env:           endpointTestEnv(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd, "model_provider='ao_endpoint'") {
		t.Fatalf("launch command lacks the endpoint provider: %v", cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), "sk-endpoint-secret") {
		t.Fatalf("launch command exposes the key: %v", cmd)
	}
}

// A caller without a prepared launch environment reads the endpoint store.
func TestLaunchCommandReadsEndpointFromStoreWithoutEnv(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("codex", harnessendpoint.Input{BaseURL: "https://gateway.example.com/v1", APIKey: "sk-stored"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:     "review-1",
		WorkspacePath: canonicalTempDir(t),
		DataDir:       dataDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd, "model_providers.ao_endpoint.base_url='https://gateway.example.com/v1'") {
		t.Fatalf("launch command lacks the stored endpoint: %v", cmd)
	}
}

func TestLaunchAndRestoreRejectUnreadableEndpointWithoutPreparedEnv(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "harness-endpoints", "codex", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := &Plugin{resolvedBinary: "codex"}
	workspace := canonicalTempDir(t)
	launch := ports.LaunchConfig{SessionID: "session-1", WorkspacePath: workspace, DataDir: dataDir}
	if cmd, err := plugin.GetLaunchCommand(t.Context(), launch); err == nil || cmd != nil {
		t.Fatalf("launch should refuse an unreadable endpoint, got argv=%v, err=%v", cmd, err)
	}
	restore := ports.RestoreConfig{
		DataDir: dataDir,
		Session: ports.SessionRef{
			ID: "session-1", WorkspacePath: workspace,
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "thread-123"},
		},
	}
	if cmd, ok, err := plugin.GetRestoreCommand(t.Context(), restore); err == nil || cmd != nil || ok {
		t.Fatalf("restore should refuse an unreadable endpoint, got argv=%v, ok=%t, err=%v", cmd, ok, err)
	}
	launch.Env = map[string]string{}
	if cmd, err := plugin.GetLaunchCommand(t.Context(), launch); err != nil || len(cmd) == 0 {
		t.Fatalf("a prepared empty endpoint snapshot must allow launch, got argv=%v, err=%v", cmd, err)
	}
}

// A prepared env wins over the saved endpoint, so a project's own gateway is
// what argv names when the process env carries the project's key.
func TestRestoreCommandPrefersPreparedEnvOverSavedEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("codex", harnessendpoint.Input{BaseURL: "https://saved.example.com/v1", APIKey: "sk-saved"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: dataDir,
		Session: ports.SessionRef{
			ID: "session-1", WorkspacePath: canonicalTempDir(t),
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "thread-123"},
		},
		Env: map[string]string{
			harnessendpoint.CodexBaseURLEnv: "https://project.example.com/v1",
			harnessendpoint.CodexAPIKeyEnv:  "sk-project",
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore: argv=%v ok=%t err=%v", cmd, ok, err)
	}
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "https://project.example.com/v1") || strings.Contains(joined, "saved.example.com") {
		t.Fatalf("restore argv must name the prepared gateway only: %v", cmd)
	}
	if strings.Contains(joined, "sk-project") || strings.Contains(joined, "sk-saved") {
		t.Fatalf("restore argv exposes a key: %v", cmd)
	}
}
