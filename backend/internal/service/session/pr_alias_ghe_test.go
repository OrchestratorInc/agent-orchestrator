package session

import "testing"

func TestGitHubPRURLIdentityAcceptsGHECloudTenant(t *testing.T) {
	host, owner, repo, n, ok := githubPRURLIdentity("https://Acme.ghe.com/Team/App/pull/7")
	if !ok || host != "acme.ghe.com" || owner != "team" || repo != "app" || n != 7 {
		t.Fatalf("githubPRURLIdentity = %q %q %q %d %v", host, owner, repo, n, ok)
	}
	if _, _, _, _, ok := githubPRURLIdentity("https://gitlab.com/team/app/pull/7"); ok {
		t.Fatal("gitlab.com accepted as GitHub")
	}
}
