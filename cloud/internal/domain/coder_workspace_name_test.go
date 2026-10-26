package domain

import (
	"strings"
	"testing"
)

func TestCoderWorkspaceName(t *testing.T) {
	t.Parallel()
	const sessionID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	const id = "1a2b3c4d5e6f"
	longest := strings.Repeat("p", MaxCoderWorkspaceNamePrefix)
	for _, tc := range []struct {
		name, prefix, kind, displayName, want string
	}{
		{"orchestrator", "acme", "orchestrator", "Orchestrator", "acme-o-" + id},
		{"orchestrator ignores its display name", "acme", "orchestrator", "Fix login", "acme-o-" + id},
		{"worker takes the first word", "alice", "worker", "Campaign tracking", "alice-w-campaign-" + id},
		{"word is lower-cased alphanumerics", "acme", "worker", "  OAuth2-callback: retry", "acme-w-oauth2-" + id},
		{"leading punctuation is skipped", "acme", "worker", "#42: handle nil", "acme-w-42-" + id},
		{"word is capped", "acme", "worker", "Internationalization pass", "acme-w-internatio-" + id},
		{"no usable word is omitted", "acme", "worker", "修正 ✓", "acme-w-" + id},
		{"empty display name", "acme", "worker", "", "acme-w-" + id},
		{"empty prefix uses ao", "", "worker", "Fix", "ao-w-fix-" + id},
		{"invalid prefix uses ao", "Bad--Prefix", "orchestrator", "", "ao-o-" + id},
		{"long prefix: word shortens first, then prefix", longest, "worker", "Refactor", longest[:13] + "-w-ref-" + id},
		{"long prefix with a short word", longest, "worker", "ab", longest[:14] + "-w-ab-" + id},
		{"long prefix orchestrator", longest, "orchestrator", "", longest[:17] + "-o-" + id},
		{"long prefix without a word", longest, "worker", "", longest[:17] + "-w-" + id},
		{"prefix word shortens before the prefix", "acme-platform", "worker", "Campaign", "acme-platform-w-cam-" + id},
		{"truncated prefix drops a trailing hyphen", "abcdefghijkl-mnop", "worker", "Fix", "abcdefghijkl-w-fix-" + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CoderWorkspaceName(tc.prefix, tc.kind, tc.displayName, sessionID)
			if got != tc.want {
				t.Fatalf("CoderWorkspaceName = %q, want %q", got, tc.want)
			}
			if err := ValidateCoderWorkspaceName(got); err != nil {
				t.Fatalf("CoderWorkspaceName = %q is not a valid Coder name: %v", got, err)
			}
		})
	}
}

// The retry name keeps the prefix, role and word and fills the rest of the 32
// characters with more of the id; a first name that already used all 32 makes
// room for a 16-character id.
func TestCoderWorkspaceRetryName(t *testing.T) {
	t.Parallel()
	const sessionID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	const hex = "1a2b3c4d5e6f4a7b8c9d0e1f2a3b4c5d"
	longest := strings.Repeat("p", MaxCoderWorkspaceNamePrefix)
	for _, tc := range []struct {
		name, prefix, kind, displayName, want, wantRetry string
	}{
		{"fills the remaining length", "alice", "worker", "Campaign", "alice-w-campaign-" + hex[:12], "alice-w-campaign-" + hex[:15]},
		{"orchestrator", "acme", "orchestrator", "", "acme-o-" + hex[:12], "acme-o-" + hex[:25]},
		{"a full first name shortens the word and prefix", longest, "worker", "Refactor", longest[:13] + "-w-ref-" + hex[:12], longest[:9] + "-w-ref-" + hex[:16]},
		{"a full orchestrator name shortens the prefix", longest, "orchestrator", "", longest[:17] + "-o-" + hex[:12], longest[:13] + "-o-" + hex[:16]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, retry := CoderWorkspaceNames(tc.prefix, tc.kind, tc.displayName, sessionID)
			if name != tc.want || retry != tc.wantRetry {
				t.Fatalf("CoderWorkspaceNames = %q, %q; want %q, %q", name, retry, tc.want, tc.wantRetry)
			}
			if len(retry) != MaxCoderWorkspaceName {
				t.Fatalf("retry name %q is %d characters, want the full %d", retry, len(retry), MaxCoderWorkspaceName)
			}
		})
	}
}

// Every combination gives two distinct valid Coder names that keep the role
// marker, with a 12-character id in the first name and a longer one in the
// retry name.
func TestCoderWorkspaceNamesAlwaysFitCoderRules(t *testing.T) {
	t.Parallel()
	prefixes := []string{"", "a", "ao", "dev-kit", "a1-b2-c3", strings.Repeat("x", 19) + "y", "abcdefghijklmnopqr-s", "abcdefghijkl-mnop"}
	names := []string{"", "x", "Fix", "Refactor everything", strings.Repeat("Z", 40), "-- ++", "日本語 fix"}
	for _, prefix := range prefixes {
		for _, kind := range []string{"orchestrator", "worker"} {
			for _, displayName := range names {
				for _, sessionID := range []string{"ffffffff-0000-4000-8000-000000000000", "s-1"} {
					name, retry := CoderWorkspaceNames(prefix, kind, displayName, sessionID)
					role := "-w-"
					if kind == "orchestrator" {
						role = "-o-"
					}
					for _, got := range []string{name, retry} {
						if err := ValidateCoderWorkspaceName(got); err != nil {
							t.Fatalf("CoderWorkspaceNames(%q, %q, %q, %q) gave %q: %v", prefix, kind, displayName, sessionID, got, err)
						}
						if !strings.Contains(got, role) {
							t.Fatalf("CoderWorkspaceNames(%q, %q, %q, %q) gave %q without %s", prefix, kind, displayName, sessionID, got, role)
						}
					}
					nameID, retryID := name[strings.LastIndex(name, "-")+1:], retry[strings.LastIndex(retry, "-")+1:]
					if nameID != coderWorkspaceNameID(sessionID, 12) || len(retryID) <= len(nameID) || name == retry {
						t.Fatalf("CoderWorkspaceNames(%q, %q, %q, %q) = %q, %q: want a 12-character id and a longer retry id", prefix, kind, displayName, sessionID, name, retry)
					}
				}
			}
		}
	}
}

func TestValidateCoderWorkspaceName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ao-o-1a2b3c4d", "a", strings.Repeat("a", 32)} {
		if err := ValidateCoderWorkspaceName(name); err != nil {
			t.Errorf("ValidateCoderWorkspaceName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "-ao", "ao-", "ao--o", "Ao-o", "ao_o", strings.Repeat("a", 33)} {
		if err := ValidateCoderWorkspaceName(name); err == nil {
			t.Errorf("ValidateCoderWorkspaceName(%q) = nil, want an error", name)
		}
	}
}
