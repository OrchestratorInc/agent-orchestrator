package multi

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMPullRequestPublisher = (*Provider)(nil)

// ReconcileOrCreatePullRequest delegates publication to the repository's SCM provider.
func (m *Provider) ReconcileOrCreatePullRequest(ctx context.Context, request ports.SCMPublishRequest) (ports.SCMPublishResult, error) {
	provider, err := m.resolve(request.Repo.Provider)
	if err != nil {
		return ports.SCMPublishResult{}, err
	}
	publisher, ok := provider.(ports.SCMPullRequestPublisher)
	if !ok {
		return ports.SCMPublishResult{}, fmt.Errorf("scm multi: provider %q does not support pull request publication", request.Repo.Provider)
	}
	return publisher.ReconcileOrCreatePullRequest(ctx, request)
}
