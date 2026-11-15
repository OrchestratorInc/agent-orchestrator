package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
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

func TestPullRequestDetailsFromGraphQL(t *testing.T) {
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

	details := pullRequestDetailsFromGraphQL(pr)
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

// detailsFieldMarkers are the selections only the details read makes.
var detailsFieldMarkers = []string{"body", "labels(first:", "reviewRequests(first:", "commitList:", "conversation:"}

func assertRequestsDetails(t *testing.T, what, fields string, want bool) {
	t.Helper()
	for _, marker := range detailsFieldMarkers {
		if got := strings.Contains(fields, marker); got != want {
			t.Errorf("%s: requests %q = %v, want %v:\n%s", what, marker, got, want, fields)
		}
	}
}

var nextAlias = regexp.MustCompile(`\npr[0-9]+: `)

// aliasFields returns the part of a batch query that selects alias's PR.
func aliasFields(t *testing.T, query, alias string) string {
	t.Helper()
	start := strings.Index(query, alias+": repository(")
	if start < 0 {
		t.Fatalf("query has no alias %s:\n%s", alias, query)
	}
	rest := query[start+len(alias):]
	if loc := nextAlias.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return rest
}

// recordGraphQL answers the n-th GraphQL request with {"data": replies[n]},
// or a 502 when replies[n] is nil, and returns the queries it was sent.
func recordGraphQL(t *testing.T, fake *fakeGH, replies ...map[string]any) func() []string {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	fake.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode graphql request: %v", err)
		}
		mu.Lock()
		n := len(queries)
		queries = append(queries, req.Query)
		mu.Unlock()
		if n >= len(replies) {
			t.Errorf("unexpected graphql request %d:\n%s", n+1, req.Query)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		if replies[n] == nil {
			http.Error(w, "upstream failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": replies[n]})
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), queries...)
	}
}

// batchPRNode is a batch-query PR node with the light fields only.
func batchPRNode(number int, updatedAt string) map[string]any {
	var pr map[string]any
	basePRFixture().prData(func(m map[string]any) { pr = m })
	pr["number"] = number
	pr["url"] = fmt.Sprintf("https://github.com/octocat/hello/pull/%d", number)
	pr["updatedAt"] = updatedAt
	return pr
}

var detailsTestRepo = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}

// A ref whose stored details are as new as the PR asks only for the light
// fields: the same selection the batch query made before PRs had details.
func TestSCMBatchQueryLeavesOutDetailsTheCallerAlreadyHas(t *testing.T) {
	stored := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	query, _ := buildSCMBatchQuery([]ports.SCMPRRef{{Repo: detailsTestRepo, Number: 42, DetailsUpdatedAt: stored}})
	want := "query{\npr0: repository(owner:\"octocat\",name:\"hello\"){ pullRequest(number:42){ " + scmPRFields() + " } }\n}"
	if query != want {
		t.Fatalf("query = %s\nwant %s", query, want)
	}
	assertRequestsDetails(t, "light query", query, false)
	for _, light := range []string{"updatedAt", "statusCheckRollup", "mergeStateStatus"} {
		if !strings.Contains(query, light) {
			t.Errorf("light query is missing %q", light)
		}
	}

	detailsQuery, _ := buildSCMDetailsQuery([]ports.SCMPRRef{{Repo: detailsTestRepo, Number: 42, DetailsUpdatedAt: stored}})
	assertRequestsDetails(t, "details query", detailsQuery, true)
	if strings.Contains(detailsQuery, "statusCheckRollup") || strings.Contains(detailsQuery, "mergeStateStatus") {
		t.Errorf("details query re-reads the light fields:\n%s", detailsQuery)
	}
}

// An unchanged PR costs one light query and leaves Details nil, so the
// stored details are kept.
func TestFetchPullRequestsSkipsTheDetailsOfAnUnchangedPR(t *testing.T) {
	fake := newFakeGH(t)
	queries := recordGraphQL(t, fake, map[string]any{
		"pr0": map[string]any{"pullRequest": batchPRNode(42, "2026-11-10T10:00:00Z")},
	})
	p := newProviderForTest(t, fake)

	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{{Repo: detailsTestRepo, Number: 42, DetailsUpdatedAt: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	sent := queries()
	if len(sent) != 1 {
		t.Fatalf("graphql requests = %d, want the one light batch query", len(sent))
	}
	assertRequestsDetails(t, "batch query", sent[0], false)
	if len(obs) != 1 || !obs[0].Fetched || obs[0].PR.Details != nil {
		t.Fatalf("observation = %+v, want fetched with Details nil", obs)
	}
	if obs[0].CI.Summary != string(domain.CIPassing) || len(obs[0].CI.Checks) != 1 {
		t.Fatalf("CI = %+v, want the light fields still read", obs[0].CI)
	}
}

// One batch with an unchanged PR, a PR whose updatedAt moved and a PR with
// no stored details: the new PR's details come in the batch query, the moved
// PR's in one follow-up query, and the unchanged PR's not at all.
func TestFetchPullRequestsReadsDetailsOnlyForNewOrChangedPRs(t *testing.T) {
	stored := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	newPR := batchPRNode(3, "2026-11-10T09:00:00Z")
	newPR["body"] = "new body"
	newPR["commitList"] = map[string]any{"totalCount": float64(1), "nodes": []any{
		map[string]any{"commit": map[string]any{"oid": "ccc333", "messageHeadline": "feat: new"}},
	}}
	fake := newFakeGH(t)
	queries := recordGraphQL(t, fake,
		map[string]any{
			"pr0": map[string]any{"pullRequest": batchPRNode(1, "2026-11-10T10:00:00Z")},
			"pr1": map[string]any{"pullRequest": batchPRNode(2, "2026-11-10T11:00:00Z")},
			"pr2": map[string]any{"pullRequest": newPR},
		},
		map[string]any{
			"pr0": map[string]any{"pullRequest": map[string]any{
				"body":   "moved body",
				"labels": map[string]any{"nodes": []any{map[string]any{"name": "bug"}}},
			}},
		},
	)
	p := newProviderForTest(t, fake)

	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{
		{Repo: detailsTestRepo, Number: 1, DetailsUpdatedAt: stored},
		{Repo: detailsTestRepo, Number: 2, DetailsUpdatedAt: stored},
		{Repo: detailsTestRepo, Number: 3},
	})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	sent := queries()
	if len(sent) != 2 {
		t.Fatalf("graphql requests = %d, want the batch query and one details query", len(sent))
	}
	assertRequestsDetails(t, "unchanged PR in the batch", aliasFields(t, sent[0], "pr0"), false)
	assertRequestsDetails(t, "moved PR in the batch", aliasFields(t, sent[0], "pr1"), false)
	assertRequestsDetails(t, "new PR in the batch", aliasFields(t, sent[0], "pr2"), true)
	if !strings.Contains(sent[1], "pullRequest(number:2)") || strings.Contains(sent[1], "pr1:") {
		t.Fatalf("details query = %s, want only the moved PR #2", sent[1])
	}
	assertRequestsDetails(t, "details query", sent[1], true)

	if len(obs) != 3 || !obs[0].Fetched || !obs[1].Fetched || !obs[2].Fetched {
		t.Fatalf("observations = %+v, want all three fetched", obs)
	}
	if obs[0].PR.Details != nil {
		t.Errorf("unchanged PR details = %+v, want nil so the stored ones are kept", obs[0].PR.Details)
	}
	if d := obs[1].PR.Details; d == nil || d.Description != "moved body" || len(d.Labels) != 1 {
		t.Errorf("moved PR details = %+v, want the follow-up read", d)
	}
	if d := obs[2].PR.Details; d == nil || d.Description != "new body" || d.CommitCount != 1 {
		t.Errorf("new PR details = %+v, want the batch read", d)
	}
	if obs[1].PR.Number != 2 || len(obs[1].CI.Checks) != 1 {
		t.Errorf("moved PR observation = %+v, want its light fields intact", obs[1].PR)
	}
}

// Details are view-only: a failed details read keeps the PR's state and CI
// and leaves Details nil, so the stored ones are kept and read again next poll.
func TestFetchPullRequestsKeepsTheObservationWhenTheDetailsReadFails(t *testing.T) {
	fake := newFakeGH(t)
	queries := recordGraphQL(t, fake,
		map[string]any{"pr0": map[string]any{"pullRequest": batchPRNode(42, "2026-11-10T11:00:00Z")}},
		nil,
	)
	p := newProviderForTest(t, fake)

	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{{Repo: detailsTestRepo, Number: 42, DetailsUpdatedAt: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v, want the details failure contained", err)
	}
	if got := len(queries()); got != 2 {
		t.Fatalf("graphql requests = %d, want the batch query and the failed details query", got)
	}
	if len(obs) != 1 || !obs[0].Fetched || obs[0].PR.Details != nil || len(obs[0].CI.Checks) != 1 {
		t.Fatalf("observation = %+v, want fetched state and CI with Details nil", obs)
	}
	// The details are still owed, so the observer retries them.
	if !obs[0].PR.DetailsPending {
		t.Fatal("details read failed, but the observation isn't marked pending")
	}
}
