package domain

import "testing"

func TestRepositoryKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"git@github.com:Acme/Web.git", "github.com/acme/web"},
		{"https://github.com/acme/web", "github.com/acme/web"},
		{"https://github.com/acme/web/", "github.com/acme/web"},
		{"https://gitlab.example.com/Group/Sub/Web.git", "gitlab.example.com/Group/Sub/Web"},
		{"/srv/git/web.git/", "/srv/git/web.git"},
		{"", ""},
	} {
		if got := RepositoryKey(tc.in); got != tc.want {
			t.Errorf("RepositoryKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
