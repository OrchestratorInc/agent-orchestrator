package codex

import (
	"context"
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

// Reviewer launches pass no env; the endpoint comes from the store.
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
