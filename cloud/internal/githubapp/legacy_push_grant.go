package githubapp

import (
	"context"
	"errors"
	"time"
)

// Compatibility for workers bootstrapped before the git proxy existed. Such a
// worker keeps running its old credential helper and pull request bridge after
// the control plane is deployed, and they ask for a push token. They get one,
// minted fresh and never cached, until their sandbox is recreated with a
// current worker; the control plane decides which workers those are from what
// the worker reported at bootstrap (WorkerUsesGitProxy), which the sandbox
// cannot change. Remove this file once no pre-proxy worker is left (one
// release after the git proxy ships).

// IssueLegacyPushGrant mints the write token a pre-proxy worker asks for:
// scoped to repoFullName (the session's primary repository or a declared extra
// repository the installation can access) when the credential helper names
// one, otherwise to the primary repository plus the extras, as before.
func (s *Service) IssueLegacyPushGrant(
	ctx context.Context,
	orgID, sessionID, repoFullName string,
) (CheckoutGrant, error) {
	authorization, err := s.resolveWorkerCheckoutAuthorization(ctx, orgID, sessionID)
	if err != nil {
		return CheckoutGrant{}, err
	}
	var repositoryIDs []int64
	if repoFullName == "" {
		if err := s.requireUserScopedAccess(ctx, authorization, authorization.GitHubRepositoryID, true); err != nil {
			return CheckoutGrant{}, err
		}
		repositoryIDs = s.filterUserScopedRepositoryIDs(ctx, authorization,
			s.checkoutRepositoryIDs(ctx, orgID, sessionID, authorization), true)
	} else {
		repositoryID, err := s.pushRepositoryID(ctx, orgID, sessionID, authorization, repoFullName)
		if err != nil {
			return CheckoutGrant{}, err
		}
		if err := s.requireUserScopedAccess(ctx, authorization, repositoryID, true); err != nil {
			return CheckoutGrant{}, err
		}
		repositoryIDs = []int64{repositoryID}
	}
	access, err := s.client.legacyWriteTokenForRepos(ctx, authorization.GitHubInstallationID, repositoryIDs)
	if err != nil {
		return CheckoutGrant{}, err
	}
	if access.ExpiresAt.After(time.Now().UTC().Add(2 * time.Hour)) {
		return CheckoutGrant{}, errors.New("GitHub returned an unexpectedly long-lived installation token")
	}
	return CheckoutGrant{CloneURL: authorization.CloneURL, Token: access.Token, ExpiresAt: access.ExpiresAt}, nil
}

func (b *RemoteCheckoutBroker) IssueLegacyPushGrant(context.Context, string, string, string) (CheckoutGrant, error) {
	return CheckoutGrant{}, errRemotePushNotSupported
}

// legacyWriteTokenForRepos mints, uncached, a token that writes the
// repositories' contents and pull requests.
func (c *Client) legacyWriteTokenForRepos(
	ctx context.Context,
	installationID int64,
	repositoryIDs []int64,
) (installationAccessToken, error) {
	if installationID <= 0 || len(repositoryIDs) == 0 {
		return installationAccessToken{}, errors.New("GitHub installation token scope is invalid")
	}
	for _, id := range repositoryIDs {
		if id <= 0 {
			return installationAccessToken{}, errors.New("GitHub installation token scope is invalid")
		}
	}
	response, err := c.createInstallationToken(ctx, installationID, map[string]any{
		"repository_ids": repositoryIDs,
		"permissions": map[string]string{
			"contents":      "write",
			"pull_requests": "write",
		},
	})
	if err != nil {
		return installationAccessToken{}, err
	}
	if response.ExpiresAt.IsZero() || !response.ExpiresAt.After(c.now()) {
		return installationAccessToken{}, errors.New("GitHub returned an expired installation token")
	}
	return response, nil
}
