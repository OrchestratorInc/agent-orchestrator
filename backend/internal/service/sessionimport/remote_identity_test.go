package sessionimport

import "testing"

func TestRemoteIdentityMatchesFormsOfOneRepository(t *testing.T) {
	same := []string{
		"git@github.com:Org/Repo.git",
		"https://github.com/org/repo",
		"https://github.com/Org/Repo.git/",
		"ssh://github.com/Org/Repo",
		"ssh://git@github.com/org/repo.git",
		"ssh://git@github.com:22/org/repo.git",
		"github.com:org/repo",
		"https://user@GitHub.com/org/repo",
	}
	for _, remote := range same {
		got, ok := remoteIdentity(remote)
		if !ok || got != "github.com/org/repo" {
			t.Errorf("remoteIdentity(%q) = %q, %v; want github.com/org/repo", remote, got, ok)
		}
	}
	if a, _ := remoteIdentity("git@gitlab.com:org/repo.git"); a == "github.com/org/repo" {
		t.Error("another host matched GitHub")
	}
	if a, _ := remoteIdentity("ssh://git@example.com:2222/org/repo"); a != "example.com:2222/org/repo" {
		t.Errorf("a non-default port must stay part of the host: %q", a)
	}
	for _, local := range []string{"/srv/git/repo.git", "../origin", "repo", `C:\src\repo`} {
		if got, ok := remoteIdentity(local); ok {
			t.Errorf("local path %q read as a network remote %q", local, got)
		}
	}
}
