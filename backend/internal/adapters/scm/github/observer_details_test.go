package github

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSCMBatchQueryRequestsPullRequestDetails(t *testing.T) {
	query, _ := buildSCMBatchQuery([]ports.SCMPRRef{{
		Repo:   ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello"},
		Number: 42,
	}})
	for _, want := range []string{
		"body",
		"labels(first:20)",
		"reviewRequests(first:20)",
		"... on Team { name }",
		// Aliased so it does not collide with the check-rollup commits(last:1).
		"commitList: commits(last:50)",
		"commits(last:1)",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("batch query is missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, "_LIMIT") {
		t.Errorf("batch query left a limit placeholder unreplaced:\n%s", query)
	}
}

func TestSCMObservationCarriesPullRequestDetails(t *testing.T) {
	fx := basePRFixture()
	var pr map[string]any
	fx.prData(func(m map[string]any) {
		pr = m
		m["body"] = "## Summary\nFixes the checkpoint."
		m["labels"] = map[string]any{"nodes": []any{
			map[string]any{"name": "bug", "color": "d73a4a"},
			map[string]any{"name": "  ", "color": "ffffff"},
		}}
		m["reviewRequests"] = map[string]any{"nodes": []any{
			map[string]any{"requestedReviewer": map[string]any{"__typename": "User", "login": "whoisasx", "avatarUrl": "https://a/1"}},
			map[string]any{"requestedReviewer": map[string]any{"__typename": "Team", "name": "core"}},
			map[string]any{"requestedReviewer": nil},
		}}
		m["commitList"] = map[string]any{"totalCount": float64(120), "nodes": []any{
			map[string]any{"commit": map[string]any{
				"oid": "aaa111", "messageHeadline": "fix: settle checkpoints", "committedDate": "2026-10-20T10:00:00Z",
				"author": map[string]any{"name": "Dev One", "user": map[string]any{"login": "devone"}},
			}},
			map[string]any{"commit": map[string]any{
				"oid": "bbb222", "messageHeadline": "test: cover queue", "committedDate": "2026-10-20T11:00:00Z",
				// A commit made with an email GitHub cannot link has no user.
				"author": map[string]any{"name": "Unlinked Dev", "user": nil},
			}},
		}}
	})

	obs := scmObservationFromGraphQL(ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 42}, pr)
	details := obs.PR.Details
	if details == nil {
		t.Fatal("Details is nil")
	}
	if details.Description != "## Summary\nFixes the checkpoint." || details.DescriptionTruncated {
		t.Errorf("description = %q (truncated %v)", details.Description, details.DescriptionTruncated)
	}
	if got, want := details.Labels, []domain.PullRequestLabel{{Name: "bug", Color: "d73a4a"}}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("labels = %+v, want %+v (blank-named label dropped)", got, want)
	}
	wantReviewers := []domain.PullRequestRequestedReviewer{{Login: "whoisasx", AvatarURL: "https://a/1"}, {Login: "core", IsTeam: true}}
	if len(details.RequestedReviewers) != 2 || details.RequestedReviewers[0] != wantReviewers[0] || details.RequestedReviewers[1] != wantReviewers[1] {
		t.Errorf("requested reviewers = %+v, want %+v", details.RequestedReviewers, wantReviewers)
	}
	if details.CommitCount != 120 || len(details.Commits) != 2 {
		t.Fatalf("commit count = %d, listed = %d; want 120 total and 2 listed", details.CommitCount, len(details.Commits))
	}
	first, second := details.Commits[0], details.Commits[1]
	if first.SHA != "aaa111" || first.Author != "devone" || first.Headline != "fix: settle checkpoints" ||
		!first.CommittedAt.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("first commit = %+v", first)
	}
	if second.Author != "Unlinked Dev" {
		t.Errorf("second commit author = %q, want the git author name when no GitHub user is linked", second.Author)
	}
}

// A PR with no labels, reviewers or commits listed still reports details, with
// empty lists, so the stored copy is replaced and the wire shape stays [].
func TestSCMObservationDetailsDefaultToEmptyLists(t *testing.T) {
	details := pullRequestDetailsFromGraphQL(map[string]any{})
	if details == nil || details.Labels == nil || details.RequestedReviewers == nil || details.Commits == nil {
		t.Fatalf("details = %+v, want non-nil empty lists", details)
	}
	if details.Description != "" || details.CommitCount != 0 {
		t.Fatalf("details = %+v, want zero values", details)
	}
}

func TestTruncateDescriptionDoesNotSplitACharacter(t *testing.T) {
	short, truncated := truncateDescription("hello")
	if short != "hello" || truncated {
		t.Fatalf("short body = %q (truncated %v)", short, truncated)
	}
	// "é" is two bytes; a body of cap-1 ASCII bytes followed by it straddles the cap.
	body := strings.Repeat("a", domain.MaxPullRequestDescriptionBytes-1) + "é" + "tail"
	cut, truncated := truncateDescription(body)
	if !truncated {
		t.Fatal("long body was not reported truncated")
	}
	if !utf8.ValidString(cut) || len(cut) > domain.MaxPullRequestDescriptionBytes {
		t.Fatalf("cut body is invalid or too long (%d bytes, valid=%v)", len(cut), utf8.ValidString(cut))
	}
	if len(cut) != domain.MaxPullRequestDescriptionBytes-1 {
		t.Fatalf("cut at %d bytes, want %d (the split character dropped whole)", len(cut), domain.MaxPullRequestDescriptionBytes-1)
	}
}

func TestCommitCountNeverBelowTheListedCommits(t *testing.T) {
	details := pullRequestDetailsFromGraphQL(map[string]any{"commitList": map[string]any{
		"totalCount": float64(0),
		"nodes":      []any{map[string]any{"commit": map[string]any{"oid": "abc", "messageHeadline": "x"}}},
	}})
	if details.CommitCount != 1 {
		t.Fatalf("commit count = %d, want at least the 1 listed", details.CommitCount)
	}
}
