package githubapp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// A user-scoped installation connection (domain.GitHubAccessScopeUser) lets a
// GitHub organization member who is not an owner use the AO GitHub App on the
// repositories they can access, in their personal AO organization. Its
// authority is always that one user's GitHub authorization:
//
//   - every sync lists repositories with the user's token, so the grants are
//     GitHub's intersection of the installation's selection and the user's
//     access, never the whole installation;
//   - every checkout or push token is preceded by a live check that the user
//     can still read (or push to) each repository it covers;
//   - when the user's GitHub authorization is gone or no longer reaches the
//     installation, a sync revokes every grant and token issuance is refused.

// errUserScopeUnavailable is a user-scoped connection whose user's GitHub
// authorization is gone (disconnected, expired, or revoked on GitHub).
// userScopedResyncAfter is how stale a user-scoped connection's repository list
// may get before listing installations refreshes it.
const userScopedResyncAfter = 5 * time.Minute

var errUserScopeUnavailable = errors.New("the GitHub authorization behind this connection is no longer available")

// userScopeToken returns the GitHub user token a user-scoped connection acts
// with.
func (s *Service) userScopeToken(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", errUserScopeUnavailable
	}
	_, token, err := s.githubUserAccessToken(ctx, userID)
	if errors.Is(err, postgres.ErrNotFound) {
		return "", errUserScopeUnavailable
	}
	return token, err
}

// userScopedRepositories lists what a user-scoped connection may hold. A user
// whose authorization is gone, or who can no longer reach the installation,
// holds nothing: the sync then revokes every grant (fail closed). Other errors
// are returned so a transient GitHub failure does not revoke anything.
func (s *Service) userScopedRepositories(
	ctx context.Context,
	installation domain.GitHubInstallation,
) ([]Repository, error) {
	token, err := s.userScopeToken(ctx, installation.ScopeUserID)
	if errors.Is(err, errUserScopeUnavailable) {
		s.logger.Info("GitHub user authorization gone; revoking user-scoped repositories",
			"org_id", installation.OrgID, "installation_id", installation.GitHubInstallationID)
		return []Repository{}, nil
	}
	if err != nil {
		return nil, err
	}
	repositories, err := s.client.ListUserInstallationRepositories(ctx, token, installation.GitHubInstallationID)
	var httpError *HTTPError
	if errors.As(err, &httpError) {
		switch httpError.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			s.logger.Info("GitHub user no longer reaches the installation; revoking user-scoped repositories",
				"org_id", installation.OrgID, "installation_id", installation.GitHubInstallationID,
				"status", httpError.StatusCode)
			return []Repository{}, nil
		}
	}
	return repositories, err
}

// requireUserScopedAccess refuses a checkout (needPush false) or push (needPush
// true) of a repository through a user-scoped connection unless its user can
// still do so on GitHub. A whole-installation connection passes unchanged.
func (s *Service) requireUserScopedAccess(
	ctx context.Context,
	authorization domain.GitHubCheckoutContext,
	repositoryID int64,
	needPush bool,
) error {
	if authorization.AccessScope != domain.GitHubAccessScopeUser {
		return nil
	}
	token, err := s.userScopeToken(ctx, authorization.ScopeUserID)
	if errors.Is(err, errUserScopeUnavailable) {
		return postgres.ErrForbidden
	}
	if err != nil {
		return err
	}
	pull, push, err := s.client.UserRepositoryAccess(ctx, token, repositoryID)
	if err != nil {
		return err
	}
	if !pull || (needPush && !push) {
		return postgres.ErrForbidden
	}
	return nil
}

// filterUserScopedRepositoryIDs keeps, for a user-scoped connection, only the
// repositories its user can read (or push to). The primary repository is
// assumed already checked by the caller and is always kept first. A check that
// fails drops the repository rather than broadening the token.
func (s *Service) filterUserScopedRepositoryIDs(
	ctx context.Context,
	authorization domain.GitHubCheckoutContext,
	ids []int64,
	needPush bool,
) []int64 {
	if authorization.AccessScope != domain.GitHubAccessScopeUser || len(ids) <= 1 {
		return ids
	}
	kept := ids[:1:1]
	for _, id := range ids[1:] {
		if err := s.requireUserScopedAccess(ctx, authorization, id, needPush); err != nil {
			s.logger.Info("extra repository left out of a user-scoped token",
				"org_id", authorization.OrgID, "session_id", authorization.SessionID,
				"repository_id", id, "error", err)
			continue
		}
		kept = append(kept, id)
	}
	return kept
}

// ConnectUserInstallations connects every GitHub App installation the user's
// GitHub authorization can see to the organization: as a whole-installation
// connection when the user administers it (their own account, or an owner of
// the GitHub organization), otherwise user-scoped. Each connection is synced
// before returning. Installations that cannot be connected are skipped; the
// call fails only when none could be, with the first reason.
func (s *Service) ConnectUserInstallations(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
) ([]domain.GitHubInstallation, error) {
	if principal.UserID == "" {
		return nil, postgres.ErrInvalid
	}
	_, token, err := s.githubUserAccessToken(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	return s.connectVisibleInstallations(ctx, principal, orgID, token, 0)
}

// connectVisibleInstallations connects every installation the token's user can
// see except skip (0: none).
func (s *Service) connectVisibleInstallations(
	ctx context.Context,
	principal domain.Principal,
	orgID, token string,
	skip int64,
) ([]domain.GitHubInstallation, error) {
	visible, err := s.client.ListUserInstallations(ctx, token)
	if err != nil {
		return nil, err
	}
	connected := make([]domain.GitHubInstallation, 0, len(visible))
	var firstErr error
	for _, candidate := range visible {
		if candidate.ID == skip {
			continue
		}
		installation, err := s.connectUserInstallation(ctx, principal, orgID, token, candidate.ID)
		if err != nil {
			s.logger.Info("GitHub installation not connected", "org_id", orgID,
				"installation_id", candidate.ID, "account", candidate.Account.Login, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		connected = append(connected, installation)
	}
	if len(connected) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return connected, nil
}

// connectUserInstallation connects one installation the user can see, choosing
// its scope from whether the user administers it, and syncs its repositories.
func (s *Service) connectUserInstallation(
	ctx context.Context,
	principal domain.Principal,
	orgID, token string,
	githubInstallationID int64,
) (domain.GitHubInstallation, error) {
	providerInstallation, err := s.client.GetInstallation(ctx, githubInstallationID)
	if err != nil {
		return domain.GitHubInstallation{}, err
	}
	administers, err := s.userAdministersInstallation(ctx, token, providerInstallation)
	if err != nil {
		return domain.GitHubInstallation{}, err
	}
	installation := toDomainInstallation(providerInstallation)
	if administers {
		installation.AccessScope = domain.GitHubAccessScopeInstallation
	} else {
		installation.AccessScope = domain.GitHubAccessScopeUser
		installation.ScopeUserID = principal.UserID
	}
	installation, err = s.store.BindGitHubInstallation(ctx, principal, orgID, installation)
	if err != nil {
		return domain.GitHubInstallation{}, err
	}
	if err := s.sync(ctx, installation); err != nil {
		return domain.GitHubInstallation{}, err
	}
	installation.SyncStatus = "ready"
	return installation, nil
}

// repositoryScopeStore resolves the access scope of the connection granting a
// repository by name. The Postgres store implements it.
type repositoryScopeStore interface {
	GitHubRepositoryAccessScope(ctx context.Context, orgID, repository string) (scope, scopeUserID string, err error)
}

// requireUserScopedPush refuses a write to a repository addressed by name
// (merging a pull request, publishing a review) through a user-scoped
// connection unless its user can still push to it on GitHub.
func (s *Service) requireUserScopedPush(ctx context.Context, orgID, repository string, repositoryID int64) error {
	scoper, ok := s.store.(repositoryScopeStore)
	if !ok {
		return nil
	}
	scope, scopeUserID, err := scoper.GitHubRepositoryAccessScope(ctx, orgID, repository)
	if err != nil {
		return err
	}
	return s.requireUserScopedAccess(ctx, domain.GitHubCheckoutContext{
		OrgID: orgID, AccessScope: scope, ScopeUserID: scopeUserID,
	}, repositoryID, true)
}

// userAdministersInstallation reports whether the user administers the
// installation (their own account, or an owner of the GitHub organization).
// Only user and organization accounts can be connected at all. When the App
// cannot prove administration (an organization installation without the
// members permission) the answer is no, which connects it user-scoped rather
// than refusing it.
func (s *Service) userAdministersInstallation(
	ctx context.Context,
	token string,
	installation Installation,
) (bool, error) {
	switch installation.Account.Type {
	case "User", "Organization":
	default:
		return false, postgres.ErrForbidden
	}
	if !InstallationSupportsAuthorityProof(installation) {
		return false, nil
	}
	return s.client.UserCanAdministerInstallation(ctx, token, installation)
}

// connectOtherInstallationsTimeout bounds the follow-up connects the
// installation callback runs after its own installation is stored.
const connectOtherInstallationsTimeout = 2 * time.Minute

// connectOtherUserInstallations runs after the App installation callback has
// connected the installation the user chose. It keeps the user's GitHub
// authorization (so user-scoped connections can sync and re-check access
// later) and connects every other installation that authorization can see:
// whole where the user administers it, otherwise only the repositories they
// can access. It is best effort and never fails the callback; it outlives a
// closed browser tab, bounded by connectOtherInstallationsTimeout.
func (s *Service) connectOtherUserInstallations(
	ctx context.Context,
	orgID, userID string,
	connectedInstallationID int64,
	token UserAccessToken,
) {
	if orgID == "" || userID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), connectOtherInstallationsTimeout)
	defer cancel()
	log := s.logger.With("org_id", orgID, "user_id", userID)
	user, err := s.client.GetUser(ctx, token.Token())
	if err != nil {
		log.Warn("read GitHub user after installation", "error", err)
		return
	}
	input, err := s.encryptGitHubUserConnection(userID, user, token)
	if err != nil {
		log.Warn("encrypt GitHub user authorization", "error", err)
		return
	}
	if _, err := s.store.SaveGitHubUserConnection(ctx, userID, input); err != nil {
		log.Warn("keep GitHub user authorization", "error", err)
		return
	}
	connected, err := s.connectVisibleInstallations(ctx, domain.Principal{UserID: userID}, orgID,
		token.Token(), connectedInstallationID)
	if err != nil {
		log.Info("other GitHub installations not connected", "error", err)
		return
	}
	if len(connected) > 0 {
		log.Info("connected other GitHub installations the user can access", "count", len(connected))
	}
}
