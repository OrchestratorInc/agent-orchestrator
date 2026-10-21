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
//     installation, a sync revokes every grant and token issuance is refused;
//   - it holds only while that user is the organization's only active member.
//     Anyone who joins later would reach repositories through someone else's
//     GitHub access, so then the grants are hidden, tokens are refused and the
//     next sync revokes everything.

const (
	// userScopedResyncAfter is how stale a user-scoped connection's repository
	// list may get before listing installations refreshes it.
	userScopedResyncAfter = 5 * time.Minute
	// userScopedRefreshTimeout bounds one background refresh.
	userScopedRefreshTimeout = time.Minute
	// userAccessCacheTTL is how long a positive live access check is reused.
	// The worker's git credential helper asks for a token on every git
	// operation; this keeps those from each calling GitHub, at the cost of a
	// revocation taking up to this long to apply to new tokens.
	userAccessCacheTTL = 30 * time.Second
	// userAccessCacheMax bounds the cache; expired entries are dropped when it
	// fills.
	userAccessCacheMax = 4096
)

// errUserScopeUnavailable is a user-scoped connection whose user's GitHub
// authorization is gone (disconnected, expired, or revoked on GitHub).
var errUserScopeUnavailable = errors.New("the GitHub authorization behind this connection is no longer available")

type userAccessKey struct {
	userID       string
	repositoryID int64
}

type userAccessEntry struct {
	pull, push bool
	checkedAt  time.Time
}

// userScopeIntact reports whether a user-scoped connection's user is still the
// organization's only active member.
func (s *Service) userScopeIntact(ctx context.Context, orgID, userID string) (bool, error) {
	if orgID == "" || userID == "" {
		return false, nil
	}
	return s.store.GitHubUserScopeIntact(ctx, orgID, userID)
}

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
// holds nothing: the sync then revokes every grant (fail closed). So does an
// organization the user no longer has to themselves. Other errors, rate limits
// included, are returned so a transient failure does not revoke anything.
func (s *Service) userScopedRepositories(
	ctx context.Context,
	installation domain.GitHubInstallation,
) ([]Repository, error) {
	intact, err := s.userScopeIntact(ctx, installation.OrgID, installation.ScopeUserID)
	if err != nil {
		return nil, err
	}
	if !intact {
		s.logger.Info("organization no longer has the connecting user as its only member; revoking user-scoped repositories",
			"org_id", installation.OrgID, "installation_id", installation.GitHubInstallationID)
		return []Repository{}, nil
	}
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
	if errors.As(err, &httpError) && !httpError.RateLimited {
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
	intact, err := s.userScopeIntact(ctx, authorization.OrgID, authorization.ScopeUserID)
	if err != nil {
		return err
	}
	if !intact {
		return postgres.ErrForbidden
	}
	pull, push, err := s.userRepositoryAccess(ctx, authorization.ScopeUserID, repositoryID)
	if err != nil {
		return err
	}
	if !pull || (needPush && !push) {
		return postgres.ErrForbidden
	}
	return nil
}

// userRepositoryAccess is the user's live access to a repository, reusing a
// positive GitHub answer for userAccessCacheTTL. The user's stored
// authorization is always read first, so disconnecting GitHub applies at once;
// a denial is never cached, so access granted on GitHub applies at once too.
func (s *Service) userRepositoryAccess(ctx context.Context, userID string, repositoryID int64) (pull, push bool, err error) {
	token, err := s.userScopeToken(ctx, userID)
	if errors.Is(err, errUserScopeUnavailable) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	key := userAccessKey{userID: userID, repositoryID: repositoryID}
	now := time.Now()
	s.userAccessMu.Lock()
	entry, ok := s.userAccess[key]
	s.userAccessMu.Unlock()
	if ok && now.Sub(entry.checkedAt) < userAccessCacheTTL {
		return entry.pull, entry.push, nil
	}
	pull, push, err = s.client.UserRepositoryAccess(ctx, token, repositoryID)
	if err != nil {
		return false, false, err
	}
	s.userAccessMu.Lock()
	defer s.userAccessMu.Unlock()
	if !pull {
		delete(s.userAccess, key)
		return false, false, nil
	}
	if s.userAccess == nil {
		s.userAccess = make(map[userAccessKey]userAccessEntry)
	}
	if len(s.userAccess) >= userAccessCacheMax {
		for cached, value := range s.userAccess {
			if now.Sub(value.checkedAt) >= userAccessCacheTTL {
				delete(s.userAccess, cached)
			}
		}
		if len(s.userAccess) >= userAccessCacheMax {
			clear(s.userAccess)
		}
	}
	s.userAccess[key] = userAccessEntry{pull: pull, push: push, checkedAt: now}
	return pull, push, nil
}

// refreshUserScopedInBackground re-syncs a stale user-scoped connection off
// the request path. At most one refresh per connection runs per
// userScopedResyncAfter in this process, whether or not the last one worked,
// so a failing one is not retried on every poll.
func (s *Service) refreshUserScopedInBackground(ctx context.Context, installation domain.GitHubInstallation) {
	if !installation.UserScoped() || (installation.LastSyncedAt != nil &&
		time.Since(*installation.LastSyncedAt) <= userScopedResyncAfter) {
		return
	}
	now := time.Now()
	s.userScopeRefreshMu.Lock()
	if last, ok := s.userScopeRefreshAt[installation.ID]; ok && now.Sub(last) < userScopedResyncAfter {
		s.userScopeRefreshMu.Unlock()
		return
	}
	if s.userScopeRefreshAt == nil {
		s.userScopeRefreshAt = make(map[string]time.Time)
	}
	for id, last := range s.userScopeRefreshAt {
		if now.Sub(last) >= userScopedResyncAfter {
			delete(s.userScopeRefreshAt, id)
		}
	}
	s.userScopeRefreshAt[installation.ID] = now
	s.userScopeRefreshMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), userScopedRefreshTimeout)
		defer cancel()
		if err := s.sync(ctx, installation); err != nil {
			s.logger.Warn("refresh user-scoped GitHub installation", "error", err,
				"org_id", installation.OrgID, "installation_id", installation.GitHubInstallationID)
		}
	}()
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
// call fails only when none could be, with the first reason. The organization
// must be the user's alone.
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
// see except skip (0: none), into an organization where the user is the only
// member.
func (s *Service) connectVisibleInstallations(
	ctx context.Context,
	principal domain.Principal,
	orgID, token string,
	skip int64,
) ([]domain.GitHubInstallation, error) {
	// Connecting installations nobody picked shares them with everyone in the
	// organization, so it is only done where the user is the only member.
	intact, err := s.userScopeIntact(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if !intact {
		return nil, postgres.ErrGitHubUserScopeNeedsPersonalOrg
	}
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
	installation, err := s.scopedInstallation(ctx, principal, token, providerInstallation)
	if err != nil {
		return domain.GitHubInstallation{}, err
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

// scopedInstallation is providerInstallation as a connection for principal,
// scoped by whether they administer it: whole when they do, otherwise only
// what they can access. Every path that connects an installation decides this
// explicitly, so none widens a member's connection to the whole installation.
func (s *Service) scopedInstallation(
	ctx context.Context,
	principal domain.Principal,
	token string,
	providerInstallation Installation,
) (domain.GitHubInstallation, error) {
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
		// Without a way to tell the connection's scope, refuse rather than
		// write through what might be someone else's access.
		return postgres.ErrForbidden
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
