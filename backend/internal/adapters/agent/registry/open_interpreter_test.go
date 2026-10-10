package registry

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestOpenInterpreterHasOneProductionAdapter(t *testing.T) {
	count := 0
	for _, item := range Harnessed() {
		if item.Harness == domain.HarnessOpenInterpreter {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Open Interpreter registrations = %d", count)
	}
}
