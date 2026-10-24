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

// Documents the lossy URL slug would merge stay separate documents.
func TestArtifactShareDocumentKeyKeepsSlugCollisionsApart(t *testing.T) {
	long := strings.Repeat("abcdefghij", 10)
	pairs := []struct {
		name string
		a, b [3]string
	}{
		{"component boundary", [3]string{"a-b", "c", "index.html"}, [3]string{"a", "b-c", "index.html"}},
		{"boundary into the name", [3]string{"a", "b", "c/index.html"}, [3]string{"a", "b/c", "index.html"}},
		{"case", [3]string{"Proj", "Feat", "index.html"}, [3]string{"proj", "feat", "index.html"}},
		{"punctuation", [3]string{"my_project", "login page", "index.html"}, [3]string{"my-project", "login-page", "index.html"}},
		{"past the slug budget", [3]string{long + "x", "f", "index.html"}, [3]string{long + "y", "f", "index.html"}},
		{"empty parts", [3]string{"", "ab", "index.html"}, [3]string{"ab", "", "index.html"}},
	}
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			slugA := ArtifactShareSlug(pair.a[0], pair.a[1], "me@example.com")
			slugB := ArtifactShareSlug(pair.b[0], pair.b[1], "me@example.com")
			keyA := ArtifactShareDocumentKey(pair.a[0], pair.a[1], pair.a[2])
			keyB := ArtifactShareDocumentKey(pair.b[0], pair.b[1], pair.b[2])
			if keyA == keyB {
				t.Fatalf("documents %q and %q share key %s (slugs %q, %q)", pair.a, pair.b, keyA, slugA, slugB)
			}
		})
	}
	if ArtifactShareDocumentKey("p", "f", "index.html") != ArtifactShareDocumentKey("p", "f", "index.html") {
		t.Fatal("the same document must keep the same key")
	}
	// The slug really does merge the boundary case; only the key keeps them apart.
	if ArtifactShareSlug("a-b", "c", "me@example.com") != ArtifactShareSlug("a", "b-c", "me@example.com") {
		t.Fatal("test premise: the slug was expected to merge a-b/c and a/b-c")
	}
}
