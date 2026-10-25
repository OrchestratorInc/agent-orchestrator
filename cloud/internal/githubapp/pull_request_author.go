package githubapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// A pull request a session raises is authored by the person who started the
// session, through the GitHub App user token stored when they authorized the
// App. A guest of a shared session never acts on GitHub as themselves: the
// owner's authorization is the only one a session uses. The token is decrypted
// for this one server-side call and never leaves the control plane.

// sessionCreatorStore resolves who started a session. The Postgres store
// implements it.
type sessionCreatorStore interface {
	SessionCreatorUserID(ctx context.Context, orgID, sessionID string) (string, error)
}

// errSessionOwnerCannotAuthor means the pull request has to be opened as the
// App: the session's owner has no GitHub authorization on file, or GitHub
// refused it for this repository.
var errSessionOwnerCannotAuthor = errors.New("the session owner cannot author this pull request")

// createPullRequestAsSessionOwner opens the pull request with the session
// owner's GitHub user token. It returns errSessionOwnerCannotAuthor when that
// is not possible for a reason opening it as the App can get around; any
// other failure (an invalid branch, an existing pull request, a rate limit) is
// returned as it is, since the App would fail the same way.
func (s *Service) createPullRequestAsSessionOwner(
	ctx context.Context,
	orgID, sessionID, owner, repo string,
	input CreatePullRequestInput,
) (PullRequestResponse, error) {
	token := s.sessionOwnerGitHubToken(ctx, orgID, sessionID)
	if token == "" {
		return PullRequestResponse{}, errSessionOwnerCannotAuthor
	}
	pr, err := s.client.CreatePullRequest(ctx, token, owner, repo, input)
	if userTokenRefused(err) {
		var httpError *HTTPError
		errors.As(err, &httpError)
		s.logger.Info("GitHub refused the session owner's authorization for a pull request; opening it as the App",
			"org_id", orgID, "session_id", sessionID, "repository", owner+"/"+repo, "status", httpError.StatusCode)
		return PullRequestResponse{}, errSessionOwnerCannotAuthor
	}
	return pr, err
}

// sessionOwnerGitHubToken is the session owner's GitHub user token, refreshed
// when it is about to expire, or "" when the owner has none.
func (s *Service) sessionOwnerGitHubToken(ctx context.Context, orgID, sessionID string) string {
	creators, ok := s.store.(sessionCreatorStore)
	if !ok {
		return ""
	}
	userID, err := creators.SessionCreatorUserID(ctx, orgID, sessionID)
	if err != nil || userID == "" {
		if err != nil && !errors.Is(err, postgres.ErrNotFound) {
			s.logger.Warn("resolve session owner", "error", err, "org_id", orgID, "session_id", sessionID)
		}
		return ""
	}
	_, token, err := s.githubUserAccessToken(ctx, userID)
	if err != nil {
		if !errors.Is(err, postgres.ErrNotFound) {
			s.logger.Warn("load session owner GitHub authorization", "error", err,
				"org_id", orgID, "session_id", sessionID)
		}
		return ""
	}
	return token
}

// userTokenRefused reports whether GitHub refused a user-token request for
// lack of access (the authorization was revoked, or the user cannot reach the
// repository), as opposed to a rate limit or a bad request.
func userTokenRefused(err error) bool {
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.RateLimited {
		return false
	}
	switch httpError.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}
