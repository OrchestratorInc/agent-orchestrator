package registry

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestOpenInterpreterCandidateRemainsUnavailableInProduction(t *testing.T) {
	registry := Build(nil, nil)
	if registry.SupportsChat(domain.HarnessOpenInterpreter) {
		t.Fatal("unqualified Open Interpreter candidate exposed in Chat")
	}
	_, err := registry.Driver(domain.HarnessOpenInterpreter)
	if !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("Open Interpreter Chat admission = %v", err)
	}
}
