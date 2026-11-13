package domain

import "testing"

func TestCanonicalRepositoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name, origin, canonical string
		valid                   bool
	}{
		{"legacy", "", "", true},
		{"authenticated checkout origin", "https://git-user:token@github.com/alice/repo.git", "https://github.com/acme/repo", true},
		{"github fork", "git@github.com:alice/repo.git", "https://github.com/acme/repo", true},
		{"gitlab nested", "ssh://git@gitlab.example.com/alice/team/repo.git", "https://gitlab.example.com/group/subgroup/repo", true},
		{"cross host", "https://gitlab.com/alice/repo", "https://gitlab.example.com/group/repo", false},
		{"cross provider", "https://github.com/alice/repo", "https://gitlab.com/group/repo", false},
		{"no origin", "", "https://github.com/acme/repo", false},
		{"remote name", "https://github.com/alice/repo", "upstream", false},
		{"PR URL", "https://github.com/alice/repo", "https://github.com/acme/repo/pull/7", false},
		{"MR URL", "https://gitlab.com/alice/repo", "https://gitlab.com/acme/repo/-/merge_requests/7", false},
		{"query", "https://github.com/alice/repo", "https://github.com/acme/repo?x=y", false},
		{"credentials", "https://github.com/alice/repo", "https://user:secret@github.com/acme/repo", false},
		{"traversal", "https://gitlab.com/alice/repo", "https://gitlab.com/acme/../repo", false},
		{"encoded namespace", "https://gitlab.com/alice/repo", "https://gitlab.com/acme%2fsub/repo", false},
		{"custom authority", "https://gitlab.example.com:8443/alice/repo", "https://gitlab.example.com:8443/group/sub/repo", true},
		{"port", "https://github.com/alice/repo", "https://github.com:8443/acme/repo", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ProjectConfig{CanonicalRepoURL: tc.canonical}
			if err := cfg.ValidateCanonicalRepository(tc.origin); (err == nil) != tc.valid {
				t.Fatalf("validation = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestRepositoryProviderGHECloudTenant(t *testing.T) {
	for _, origin := range []string{
		"https://acme.ghe.com/team/app.git",
		"git@acme.ghe.com:team/app.git",
		"https://ACME.GHE.COM/team/app",
		"ssh://git@acme.ghe.com:2222/team/app.git",
	} {
		t.Run(origin, func(t *testing.T) {
			got, err := ParseRepositoryIdentity(origin)
			if err != nil {
				t.Fatalf("ParseRepositoryIdentity: %v", err)
			}
			if got.Provider != "github" || got.Namespace != "team" || got.Name != "app" {
				t.Fatalf("got %+v, want github team/app", got)
			}
		})
	}
}

func TestIsGitHubHostRejectsLookAlikes(t *testing.T) {
	for host, want := range map[string]bool{
		"github.com": true, "api.github.com": true, "corp.ghe.io": true,
		"acme.ghe.com": true, "ACME.ghe.com:443": true,
		"ghe.com": false, "api.acme.ghe.com": false, "acme.ghe.com.evil.example": false,
		"evilghe.com": false, ".ghe.com": false, "gitlab.com": false,
	} {
		if got := IsGitHubHost(host); got != want {
			t.Errorf("IsGitHubHost(%q) = %v, want %v", host, got, want)
		}
	}
	for host, want := range map[string]string{
		"acme.ghe.com": "https://api.acme.ghe.com", "Acme.GHE.com:443": "https://api.acme.ghe.com",
		"github.com": "", "corp.ghe.io": "", "api.acme.ghe.com": "", "": "",
	} {
		got, ok := GHECloudAPIBase(host)
		if got != want || ok != (want != "") {
			t.Errorf("GHECloudAPIBase(%q) = %q, %v, want %q", host, got, ok, want)
		}
	}
}
