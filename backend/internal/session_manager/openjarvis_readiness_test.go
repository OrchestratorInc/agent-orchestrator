package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type renderedReadinessAgent struct{ readinessAgent }

func (renderedReadinessAgent) RequiresRenderedActivity() bool { return true }

type renderedReadinessRuntime struct {
	*fakeRuntime
	captures int
}

func (r *renderedReadinessRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.captures++
	return "You> ", nil
}
func TestNativeReadinessUsesCurrentUnterminatedRenderedPrompt(t *testing.T) {
	runtime := &renderedReadinessRuntime{fakeRuntime: &fakeRuntime{outputs: []string{"startup banner without unterminated input row"}}}
	m := New(Deps{Runtime: runtime})
	agent := renderedReadinessAgent{readinessAgent: readinessAgent{hints: ports.PromptReadinessHints{Patterns: []string{"You>"}, Timeout: time.Second, PollInterval: time.Millisecond, Lines: 40}}}
	if err := m.waitForPromptReadiness(context.Background(), agent, ports.LaunchConfig{}, ports.RuntimeHandle{}); err != nil {
		t.Fatal(err)
	}
	if runtime.captures != 1 || runtime.outputCalls != 0 {
		t.Fatalf("rendered captures=%d raw calls=%d", runtime.captures, runtime.outputCalls)
	}
}
