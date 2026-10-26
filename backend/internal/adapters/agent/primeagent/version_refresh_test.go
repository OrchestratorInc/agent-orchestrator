package primeagent

import (
	"context"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRefreshRestorePreservesModelAndEffort(t *testing.T) {
	p := &Plugin{resolvedBinary: "prime-agent", extensionFlag: map[string]bool{"prime-agent": true}}
	argv, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: t.TempDir(), Config: ports.AgentConfig{Model: "provider/original", Effort: "high"}, Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-original"}}})
	if err != nil || !ok {
		t.Fatalf("restore: %t %v", ok, err)
	}
	for _, value := range []string{"--model", "provider/original", "--thinking", "high", "native-original"} {
		if !slices.Contains(argv, value) {
			t.Fatalf("restore lost %q: %q", value, argv)
		}
	}
}
