package githubapp

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// LocalFirstBroker is the checkout broker of an environment that runs its own
// GitHub App and also reaches production's through the remote capability
// broker (staging). A session is served by the local App; a repository the
// local App does not authorize, such as one in a project production created
// through environment control, is read through the remote broker instead.
// Writes (pull requests, pushes, reviews) are the local App's only, as the
// remote broker supports none.
type LocalFirstBroker struct {
	*Service
	Remote *RemoteCheckoutBroker
}

func notAuthorizedLocally(err error) bool {
	return errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound)
}

func (b LocalFirstBroker) IssueCheckoutGrant(ctx context.Context, orgID, sessionID string) (CheckoutGrant, error) {
	grant, err := b.Service.IssueCheckoutGrant(ctx, orgID, sessionID)
	if notAuthorizedLocally(err) && b.Remote != nil {
		return b.Remote.IssueCheckoutGrant(ctx, orgID, sessionID)
	}
	return grant, err
}

func (b LocalFirstBroker) IssueGitProxyGrant(
	ctx context.Context,
	orgID, sessionID, repoFullName string,
	push bool,
) (CheckoutGrant, error) {
	grant, err := b.Service.IssueGitProxyGrant(ctx, orgID, sessionID, repoFullName, push)
	if !push && notAuthorizedLocally(err) && b.Remote != nil {
		return b.Remote.IssueGitProxyGrant(ctx, orgID, sessionID, repoFullName, false)
	}
	return grant, err
}
