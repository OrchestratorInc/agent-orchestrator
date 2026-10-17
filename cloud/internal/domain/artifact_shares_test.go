package domain

import (
	"regexp"
	"strings"
	"testing"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func TestArtifactShareSlug(t *testing.T) {
	cases := []struct {
		project, feature, email, want string
	}{
		{"agent-orchestrator", "Login Page", "aditi1178@gmail.com", "agent-orchestrator-login-page-aditi1178-gmail-com"},
		{"My_Project", "feat/add--auth", "A.B@Example.COM", "my-project-feat-add-auth-a-b-example-com"},
		{"", "", "dev@example.com", "dev-example-com"},
		{"proj", "", "dev@example.com", "proj-dev-example-com"},
		{"", "", "", "artifact"},
		{"---", "!!!", "@@@", "artifact"},
	}
	for _, tc := range cases {
		if got := ArtifactShareSlug(tc.project, tc.feature, tc.email); got != tc.want {
			t.Errorf("ArtifactShareSlug(%q, %q, %q) = %q, want %q", tc.project, tc.feature, tc.email, got, tc.want)
		}
	}
}

func TestArtifactShareSlugFitsADNSLabelWithSuffix(t *testing.T) {
	long := strings.Repeat("abcdefghij", 10)
	for _, tc := range [][3]string{
		{long, long, "someone@example.com"},
		{"short", long, "someone@example.com"},
		{long, "short", "someone@example.com"},
		{long, long, long + "@" + long + ".com"},
		{"a-", "-b", "x"},
	} {
		slug := ArtifactShareSlug(tc[0], tc[1], tc[2])
		withSuffix := ArtifactShareSlugCandidate(slug, MaxArtifactShareSlugAttempts)
		if len(withSuffix) > 63 || !dnsLabel.MatchString(withSuffix) {
			t.Errorf("slug %q (with suffix %q, %d chars) is not a valid DNS label", slug, withSuffix, len(withSuffix))
		}
	}
	slug := ArtifactShareSlug("short", long, "someone@example.com")
	if !strings.HasPrefix(slug, "short-") || !strings.HasSuffix(slug, "-someone-example-com") {
		t.Errorf("short project lost its unused budget to the feature: %q", slug)
	}
}

func TestArtifactShareSlugCandidate(t *testing.T) {
	if got := ArtifactShareSlugCandidate("a-b", 1); got != "a-b" {
		t.Errorf("attempt 1 = %q", got)
	}
	if got := ArtifactShareSlugCandidate("a-b", 3); got != "a-b-3" {
		t.Errorf("attempt 3 = %q", got)
	}
}
