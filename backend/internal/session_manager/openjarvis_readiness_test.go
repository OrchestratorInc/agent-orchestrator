package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/openjarvis"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeReadinessUsesCurrentUnterminatedRenderedPrompt(t *testing.T) {
	hints, err := openjarvis.New().PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &renderedReadinessRuntime{
		fakeRuntime: &fakeRuntime{outputs: []string{"startup banner without unterminated input row"}},
		screens:     []string{"You> "},
	}
	m := New(Deps{Runtime: runtime})
	if err := m.waitForPromptReadiness(context.Background(), readinessAgent{hints: hints}, ports.LaunchConfig{}, ports.RuntimeHandle{}); err != nil {
		t.Fatal(err)
	}
	if runtime.styledCalls != 1 || runtime.outputCalls != 0 {
		t.Fatalf("rendered captures=%d raw calls=%d", runtime.styledCalls, runtime.outputCalls)
	}
}
