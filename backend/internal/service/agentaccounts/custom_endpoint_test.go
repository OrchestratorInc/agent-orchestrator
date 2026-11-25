package agentaccounts

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUsesCustomEndpoint(t *testing.T) {
	s := &Service{}
	if s.usesCustomEndpoint(domain.AgentAccountProviderCodex) {
		t.Fatal("no hook means no custom endpoint")
	}
	s.SetCustomEndpoint(func(p domain.AgentAccountProvider) bool { return p == domain.AgentAccountProviderCodex })
	if !s.usesCustomEndpoint(domain.AgentAccountProviderCodex) || s.usesCustomEndpoint(domain.AgentAccountProviderClaude) {
		t.Fatal("hook result not honored per provider")
	}
}
