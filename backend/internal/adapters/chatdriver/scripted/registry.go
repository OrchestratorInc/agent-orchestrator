package scripted

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Registry returns a ports.ChatDriverRegistry serving exactly the given drivers.
//
// It exists so a test can compose harnesses with deliberately different capabilities
// over one real Chat service: a harness that steers, and a harness that does not.
// That difference is the point of the registry being a lookup rather than a single
// driver — AO routes per harness, and a test that supplied one driver to every
// harness could not prove it.
//
// A harness with no driver returns ports.ErrChatUnsupported, which is what the Chat
// service already treats as "this agent cannot do Chat". It never invents a driver.
func Registry(drivers ...ports.ChatDriver) ports.ChatDriverRegistry {
	byHarness := make(map[domain.AgentHarness]ports.ChatDriver, len(drivers))
	for _, driver := range drivers {
		if driver == nil {
			continue
		}
		byHarness[driver.Harness()] = driver
	}
	return registry(byHarness)
}

// registry is the map-backed ports.ChatDriverRegistry Registry returns.
type registry map[domain.AgentHarness]ports.ChatDriver

// Driver implements ports.ChatDriverRegistry.
func (r registry) Driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	if driver, ok := r[harness]; ok {
		return driver, nil
	}
	return nil, ports.ErrChatUnsupported
}

// SupportsChat implements ports.ChatDriverRegistry. It answers from the lookup alone
// and never probes an install, so it stays cheap for a picker to call.
func (r registry) SupportsChat(harness domain.AgentHarness) bool {
	_, ok := r[harness]
	return ok
}
