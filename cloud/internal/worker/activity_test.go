package worker

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestCodexInterruptReportsIdle(t *testing.T) {
	event, ok := ActivityEventFromHook("codex", "interrupt", []byte(`{"turn_id":"turn-1"}`))
	if !ok || event.State != contract.ActivityIdle || !ValidActivityEvent(event) {
		t.Fatalf("Codex interrupt event = %+v, reported=%t; want valid idle activity", event, ok)
	}
}
