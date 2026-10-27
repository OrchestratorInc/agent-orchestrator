package worker

import (
	"net/url"
	"strconv"
	"strings"
)

// A session's git traffic to GitHub goes through the control plane: the
// sandbox never holds a GitHub credential. Git in the sandbox rewrites every
// https://github.com/ URL to the session's proxy prefix (url.<prefix>.insteadOf)
// and authenticates to the control plane with the worker token, which the
// session credential helper reads from the worker's token file. The control
// plane checks the worker and the session's repository grant, then forwards
// the request to GitHub with a credential only it holds.

// CapabilityGitProxy, in a worker's bootstrap capabilities, means its git
// reaches GitHub only through the git proxy, so it never asks for a push token.
const CapabilityGitProxy = "git.proxy"

// GitProxyRoutePrefix is the path the control plane serves the git proxy
// under, followed by <sessionId>/<owner>/<repo>.git/<git smart-HTTP path>.
const GitProxyRoutePrefix = "/api/cloud/v1/worker/git/"

// GitProxyUsername is the username git presents to the proxy; the password is
// the worker token. The proxy ignores the username.
const GitProxyUsername = "ao-worker"

// GitHubWebOrigin is the origin git URLs of GitHub repositories start with,
// the one the session's git rewrites to the proxy.
const GitHubWebOrigin = "https://github.com/"

// GitProxyBaseURL is the URL prefix that stands in for https://github.com/ in
// a session's git configuration.
func GitProxyBaseURL(publicURL, sessionID string) string {
	return strings.TrimRight(strings.TrimSpace(publicURL), "/") + GitProxyRoutePrefix +
		url.PathEscape(strings.TrimSpace(sessionID)) + "/"
}

// GitProxyConfig is the git configuration that sends a session's GitHub
// traffic through the control plane: the URL rewrite, and the session
// credential helper (after clearing any other helper) that answers the
// control plane's host with the worker token.
func GitProxyConfig(publicURL, sessionID, dataDir string) [][2]string {
	return [][2]string{
		{"url." + GitProxyBaseURL(publicURL, sessionID) + ".insteadOf", GitHubWebOrigin},
		{"credential.helper", ""},
		{"credential.helper", GitCredentialHelperPath(dataDir)},
		{"credential.useHttpPath", "true"},
	}
}

// GitProxy is how the worker's own git commands (the checkout's clone and
// fetch, the pull request bridge's push) reach GitHub: through the session's
// proxy with the session credential helper. The zero value talks to GitHub
// directly, with no credential, which only an anonymous public checkout uses.
type GitProxy struct {
	PublicURL string
	SessionID string
	DataDir   string
}

// configArgs is GitProxyConfig as git -c arguments, which take precedence
// over every configuration file, so a checkout's own configuration cannot send
// the operation elsewhere.
func (p GitProxy) configArgs() []string {
	if p.PublicURL == "" || p.SessionID == "" || p.DataDir == "" {
		return nil
	}
	var args []string
	for _, entry := range GitProxyConfig(p.PublicURL, p.SessionID, p.DataDir) {
		args = append(args, "-c", entry[0]+"="+entry[1])
	}
	return args
}

// GitIdentity is the author and committer a session's commits carry.
type GitIdentity struct {
	Name  string
	Email string
}

// orDefault is the identity, or AO's own when it is incomplete (the session's
// owner has not authorized the GitHub App).
func (identity GitIdentity) orDefault() GitIdentity {
	if strings.TrimSpace(identity.Name) == "" || strings.TrimSpace(identity.Email) == "" {
		return GitIdentity{Name: cloudGitAuthorName, Email: cloudGitAuthorEmail}
	}
	return identity
}

// GitHubUserIdentity is the commit identity GitHub attributes to a user
// without exposing their email: their login and their noreply address.
func GitHubUserIdentity(login string, githubUserID int64) GitIdentity {
	login = strings.TrimSpace(login)
	if login == "" || githubUserID <= 0 {
		return GitIdentity{}
	}
	return GitIdentity{
		Name:  login,
		Email: strconv.FormatInt(githubUserID, 10) + "+" + login + "@users.noreply.github.com",
	}
}
