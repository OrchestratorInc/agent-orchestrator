package agentaccounts

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SetCustomEndpoint late-binds whether a provider's harness runs on a custom
// API endpoint the user set. Such sessions send the endpoint's key, not the
// account's sign-in, so a signed-out default account must not move them to
// another account or block their launch.
func (s *Service) SetCustomEndpoint(configured func(domain.AgentAccountProvider) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customEndpoint = configured
}

func (s *Service) usesCustomEndpoint(p domain.AgentAccountProvider) bool {
	s.mu.Lock()
	configured := s.customEndpoint
	s.mu.Unlock()
	return configured != nil && configured(p)
}
