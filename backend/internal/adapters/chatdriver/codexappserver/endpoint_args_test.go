package codexappserver

import (
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

func TestAppServerArgsCarryCustomEndpoint(t *testing.T) {
	if got := appServerArgs(nil); !slices.Equal(got, []string{"app-server"}) {
		t.Fatalf("appServerArgs without an endpoint = %v", got)
	}
	env := envFromSlice([]string{
		harnessendpoint.CodexBaseURLEnv + "=https://gateway.example.com/v1",
		harnessendpoint.CodexAPIKeyEnv + "=sk-endpoint",
		"PATH=/usr/bin",
	})
	args := appServerArgs(env)
	if args[len(args)-1] != "app-server" || !slices.Contains(args, "model_provider='ao_endpoint'") {
		t.Fatalf("appServerArgs = %v, want endpoint overrides before app-server", args)
	}
	if strings.Contains(strings.Join(args, " "), "sk-endpoint") {
		t.Fatalf("the key must stay in env: %v", args)
	}
}
