// Package poolacp prepares an unregistered binding for native pool acp.
package poolacp

import (
	"context"
	"fmt"
	"log/slog"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New prepares a driver that rejects every launch pending native qualification.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:      domain.AgentHarness("pool"),
		Capabilities: conservativeCapabilities(),
		Configure:    configure,
	}, log)
}

func conservativeCapabilities() ports.ChatCapabilities {
	// Override every nativeacp true default; documentation is not conformance.
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: false,
		ports.ChatCapabilityTools:     false,
		ports.ChatCapabilityApprovals: false,
		ports.ChatCapabilityInterrupt: false,
		ports.ChatCapabilityResume:    false,
	}
}

func configure(ctx context.Context, _ acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	// TODO: native hook fail-open witness, auth/permission wire contract and
	// cancellation/restore qualification are prerequisites to process admission.
	return nil, nil, fmt.Errorf("%w: pool ACP native contracts are not qualified", ports.ErrChatDriverIncompatible)
}
