package registry

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestShippedChatDrivers(t *testing.T) {
	r := Build(nil, nil)

	for _, harness := range []domain.AgentHarness{
		domain.HarnessCodex,
		domain.HarnessClaudeCode,
		domain.HarnessOpenCode,
		domain.HarnessOpenCodeV2,
		domain.HarnessDroid,
		domain.HarnessGrok,
		domain.HarnessKimi,
		domain.HarnessKimchi,
		domain.HarnessPi,
		domain.HarnessCursor,
		domain.HarnessOMP,
		domain.HarnessQwen,
		domain.HarnessUnreal,
		domain.HarnessDockerAgent,
		domain.HarnessCopilot,
		domain.HarnessAgy,
		domain.HarnessHermes,
	} {
		if !r.SupportsChat(harness) {
			t.Errorf("%s has no chat driver", harness)
		}
		if _, err := r.Driver(harness); err != nil {
			t.Errorf("resolving the %s driver: %v", harness, err)
		}
	}

	for _, harness := range []domain.AgentHarness{
		domain.HarnessAider,
		domain.HarnessMiniMaxCode,
		"definitely-not-an-agent",
	} {
		if _, err := r.Driver(harness); err == nil {
			t.Errorf("%s resolved a chat driver", harness)
		} else if !errors.Is(err, ports.ErrChatUnsupported) {
			t.Errorf("%s refused with %v, want ErrChatUnsupported so callers can branch", harness, err)
		}
	}
}
