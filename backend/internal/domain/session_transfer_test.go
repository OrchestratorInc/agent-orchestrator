package domain

import "testing"

func TestRepositoryKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"git@github.com:Acme/Web.git", "github.com/acme/web"},
		{"https://github.com/acme/web", "github.com/acme/web"},
		{"https://github.com/acme/web/", "github.com/acme/web"},
		{"https://gitlab.example.com/Group/Sub/Web.git", "gitlab.example.com/Group/Sub/Web"},
		{"https://me:ghp_secret@github.com/acme/web.git?token=ghp_secret#x", "github.com/acme/web"},
		{"https://me:ghp_secret@host/one-part?token=ghp_secret", "https://host/one-part"},
		{"/srv/git/web.git/", "/srv/git/web.git"},
		{"", ""},
	} {
		if got := RepositoryKey(tc.in); got != tc.want {
			t.Errorf("RepositoryKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCloneURLDropsSecrets(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://me:secret@github.com/acme/web.git", "https://github.com/acme/web.git"},
		{"https://secret@github.com:8443/acme/web.git?token=secret#secret", "https://github.com:8443/acme/web.git"},
		{"https://me:p%secret@github.com/acme/web", "https://github.com/acme/web"},
		{"https://a@b:secret@github.com/acme/web", "https://github.com/acme/web"},
		{"ssh://git:secret@github.com/acme/web.git", "ssh://git@github.com/acme/web.git"},
		{"git@github.com:acme/web.git", "git@github.com:acme/web.git"},
		{"https://github.com/@acme/web", "https://github.com/@acme/web"},
		{"file:///srv/git/web.git", "file:///srv/git/web.git"},
		{"/srv/git/web.git", "/srv/git/web.git"},
		{"", ""},
	} {
		if got := CloneURL(tc.in); got != tc.want {
			t.Errorf("CloneURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
