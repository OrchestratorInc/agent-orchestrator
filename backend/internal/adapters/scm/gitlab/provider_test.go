package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func testServer(t *testing.T, handler http.Handler) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(ClientOptions{
		Token:    StaticTokenSource("test-token"),
		RESTBase: srv.URL + "/api/v4",
	})
	p, err := NewProvider(ProviderOptions{Client: c})
	if err != nil {
		t.Fatal(err)
	}
	return srv, p
}

func TestParseRepository(t *testing.T) {
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.mycompany.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		remote string
		want   ports.SCMRepo
		ok     bool
	}{
		{
			name:   "ssh gitlab.com",
			remote: "git@gitlab.com:myorg/myrepo.git",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"},
			ok:     true,
		},
		{
			name:   "https gitlab.com",
			remote: "https://gitlab.com/myorg/myrepo.git",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"},
			ok:     true,
		},
		{
			name:   "https without .git",
			remote: "https://gitlab.com/myorg/myrepo",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"},
			ok:     true,
		},
		{
			name:   "self-hosted with gitlab in hostname",
			remote: "git@gitlab.mycompany.com:team/project.git",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.mycompany.com", Owner: "team", Name: "project", Repo: "team/project"},
			ok:     true,
		},
		{
			name:   "github.com rejected",
			remote: "git@github.com:owner/repo.git",
			ok:     false,
		},
		{
			name:   "empty string",
			remote: "",
			ok:     false,
		},
		{
			name:   "bare owner/repo without host context",
			remote: "myorg/myrepo",
			ok:     false,
		},
		{
			name:   "ssh nested namespace",
			remote: "git@gitlab.com:group/subgroup/repo.git",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "group/subgroup", Name: "repo", Repo: "group/subgroup/repo"},
			ok:     true,
		},
		{
			name:   "https nested namespace",
			remote: "https://gitlab.com/group/subgroup/repo.git",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "group/subgroup", Name: "repo", Repo: "group/subgroup/repo"},
			ok:     true,
		},
		{
			name:   "ssh self-managed nested namespace",
			remote: "git@gitlab.mycompany.com:eng/team/widget.git",
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.mycompany.com", Owner: "eng/team", Name: "widget", Repo: "eng/team/widget"},
			ok:     true,
		},
		{
			name:   "non-allowlisted self-managed host rejected",
			remote: "git@gitlab.attacker.example:team/project.git",
			ok:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, ok := p.ParseRepository(tt.remote)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if repo != tt.want {
				t.Errorf("repo = %+v, want %+v", repo, tt.want)
			}
		})
	}
}

func TestIsHostAllowed(t *testing.T) {
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.mycompany.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host string
		want bool
	}{
		{"gitlab.com", true},
		{"www.gitlab.com", true},
		{"gitlab.mycompany.com", true},
		{"gitlab.attacker.example", false},
		{"github.com", false},
		{"api.github.com", false},
		{"something.ghe.io", false},
		{"example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := p.isHostAllowed(tt.host)
			if got != tt.want {
				t.Errorf("isHostAllowed(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestRepoPRListGuard(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		fmt.Fprint(w, `[{"iid": 1}]`)
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	res, err := p.RepoPRListGuard(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NotModified {
		t.Error("expected not NotModified on first call")
	}
	if res.ETag != `"etag-1"` {
		t.Errorf("ETag = %q, want %q", res.ETag, `"etag-1"`)
	}

	res, err = p.RepoPRListGuard(context.Background(), repo, `"etag-1"`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NotModified {
		t.Error("expected NotModified on second call")
	}
}

func TestListPRsByRepo(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		mrs := []map[string]any{
			{
				"iid":           1,
				"title":         "Fix bug",
				"state":         "opened",
				"draft":         false,
				"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
				"source_branch": "fix-bug",
				"target_branch": "main",
				"sha":           "abc123",
				"merge_status":  "can_be_merged",
				"author":        map[string]any{"username": "alice"},
				"created_at":    now.Format(time.RFC3339),
				"updated_at":    now.Format(time.RFC3339),
			},
			{
				"iid":           2,
				"title":         "WIP: Draft MR",
				"state":         "opened",
				"draft":         true,
				"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/2",
				"source_branch": "draft-branch",
				"target_branch": "main",
				"sha":           "def456",
				"merge_status":  "unchecked",
				"author":        map[string]any{"username": "bob"},
				"created_at":    now.Format(time.RFC3339),
				"updated_at":    now.Format(time.RFC3339),
			},
		}
		json.NewEncoder(w).Encode(mrs)
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	prs, err := p.ListPRsByRepo(context.Background(), repo, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2", len(prs))
	}
	if prs[0].State != "open" {
		t.Errorf("pr[0].State = %q, want %q", prs[0].State, "open")
	}
	if prs[1].State != "draft" {
		t.Errorf("pr[1].State = %q, want %q", prs[1].State, "draft")
	}
	if prs[0].Author != "alice" {
		t.Errorf("pr[0].Author = %q, want %q", prs[0].Author, "alice")
	}
}

func TestFetchPullRequests(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/oldorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid":           1,
			"title":         "Fix bug",
			"state":         "opened",
			"draft":         false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug",
			"target_branch": "main",
			"sha":           "abc123",
			"merge_status":  "can_be_merged",
			"author":        map[string]any{"username": "alice"},
			"diff_refs":     map[string]any{"base_sha": "base123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/oldorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/oldorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
			{"id": 201, "name": "test", "status": "success", "web_url": "https://gitlab.com/jobs/201"},
		})
	})
	mux.HandleFunc("/api/v4/projects/oldorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 1,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "oldorg", Name: "myrepo", Repo: "oldorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/oldorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	o := obs[0]
	if !o.Fetched {
		t.Error("expected Fetched=true")
	}
	if o.Provider != "gitlab" {
		t.Errorf("Provider = %q, want %q", o.Provider, "gitlab")
	}
	if o.Repo != "myorg/myrepo" {
		t.Errorf("Repo = %q, want canonical myorg/myrepo", o.Repo)
	}
	if o.CI.Summary != "passing" {
		t.Errorf("CI.Summary = %q, want %q", o.CI.Summary, "passing")
	}
	if o.Review.Decision != "approved" {
		t.Errorf("Review.Decision = %q, want %q", o.Review.Decision, "approved")
	}
	if o.Mergeability.State != "mergeable" {
		t.Errorf("Mergeability.State = %q, want %q", o.Mergeability.State, "mergeable")
	}
	if len(o.CI.Checks) != 2 {
		t.Errorf("CI.Checks = %d, want 2", len(o.CI.Checks))
	}
	if got, want := o.PR.BaseSHA, "base123"; got != want {
		t.Errorf("PR.BaseSHA = %q, want %q", got, want)
	}
	if o.PR.URLAlias != ref.URL {
		t.Fatalf("PR.URLAlias = %q, want %q", o.PR.URLAlias, ref.URL)
	}
}

func TestMergeRequestObservationCarriesStableGlobalID(t *testing.T) {
	var mr restMR
	if err := json.Unmarshal([]byte(`{"id":9001,"iid":42,"web_url":"https://gitlab.com/group/repo/-/merge_requests/42","state":"opened"}`), &mr); err != nil {
		t.Fatal(err)
	}
	obs := mrToSCMPRObservation(ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Repo: "group/repo"}, &mr)
	if obs.ProviderID != "9001" {
		t.Fatalf("ProviderID = %q, want 9001", obs.ProviderID)
	}
}

func TestFetchPullRequests_FailingCI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "can_be_merged", "author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "failed", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
			{"id": 201, "name": "test", "status": "failed", "web_url": "https://gitlab.com/jobs/201"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	o := obs[0]
	if o.CI.Summary != "failing" {
		t.Errorf("CI.Summary = %q, want %q", o.CI.Summary, "failing")
	}
	if len(o.CI.FailedChecks) != 1 {
		t.Errorf("FailedChecks = %d, want 1", len(o.CI.FailedChecks))
	}
	if o.CI.FailedChecks[0].Name != "test" {
		t.Errorf("FailedChecks[0].Name = %q, want %q", o.CI.FailedChecks[0].Name, "test")
	}
}

func TestFetchFailedCheckLogTail(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	logContent := strings.Join(lines, "\n")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/jobs/201/trace", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, logContent)
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	check := ports.SCMCheckObservation{ProviderID: "201"}

	tail, err := p.FetchFailedCheckLogTail(context.Background(), repo, check)
	if err != nil {
		t.Fatal(err)
	}
	tailLines := strings.Split(tail, "\n")
	if len(tailLines) != ciFailureLogTailLines {
		t.Errorf("got %d lines, want %d", len(tailLines), ciFailureLogTailLines)
	}
	if tailLines[0] != "line 31" {
		t.Errorf("first tail line = %q, want %q", tailLines[0], "line 31")
	}
}

func TestFetchReviewThreads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":              "disc-1",
				"individual_note": false,
				"notes": []map[string]any{
					{
						"id":         100,
						"body":       "Please fix this",
						"system":     false,
						"resolvable": true,
						"resolved":   false,
						"author":     map[string]any{"username": "reviewer1"},
						"position":   map[string]any{"new_path": "main.go", "new_line": 42},
					},
				},
			},
			{
				"id":              "disc-2",
				"individual_note": false,
				"notes": []map[string]any{
					{
						"id":         101,
						"body":       "System message",
						"system":     true,
						"resolvable": false,
						"author":     map[string]any{"username": "gitlab-bot"},
					},
				},
			},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           false,
			"approvals_required": 1,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}, "approved_at": "2025-01-15T10:30:00Z"},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if review.Decision != "review_required" {
		t.Errorf("Decision = %q, want %q", review.Decision, "review_required")
	}
	if len(review.Threads) != 1 {
		t.Fatalf("Threads = %d, want 1 (system note discussion should be filtered)", len(review.Threads))
	}
	th := review.Threads[0]
	if th.Resolved {
		t.Error("expected thread to be unresolved")
	}
	if th.Path != "main.go" {
		t.Errorf("Path = %q, want %q", th.Path, "main.go")
	}
	if th.Line != 42 {
		t.Errorf("Line = %d, want %d", th.Line, 42)
	}
	if len(review.Reviews) != 1 {
		t.Fatalf("Reviews = %d, want 1", len(review.Reviews))
	}
	wantSubmittedAt := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	if !review.Reviews[0].SubmittedAt.Equal(wantSubmittedAt) {
		t.Errorf("Reviews[0].SubmittedAt = %v, want %v", review.Reviews[0].SubmittedAt, wantSubmittedAt)
	}
}

func TestFetchReviewThreads_SingleApprovalsCall(t *testing.T) {
	approvalsHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		approvalsHits++
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 2,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}, "approved_at": "2025-01-15T10:30:00Z"},
				{"user": map[string]any{"username": "reviewer2"}, "approved_at": "2025-01-15T11:00:00Z"},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if approvalsHits != 1 {
		t.Fatalf("approvals endpoint hit %d times, want exactly 1", approvalsHits)
	}
	if review.Decision != "approved" {
		t.Errorf("Decision = %q, want %q", review.Decision, "approved")
	}
	if len(review.Reviews) != 2 {
		t.Fatalf("Reviews = %d, want 2", len(review.Reviews))
	}
	wantFirst := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	if !review.Reviews[0].SubmittedAt.Equal(wantFirst) {
		t.Errorf("Reviews[0].SubmittedAt = %v, want %v", review.Reviews[0].SubmittedAt, wantFirst)
	}
	wantSecond := time.Date(2025, 1, 15, 11, 0, 0, 0, time.UTC)
	if !review.Reviews[1].SubmittedAt.Equal(wantSecond) {
		t.Errorf("Reviews[1].SubmittedAt = %v, want %v", review.Reviews[1].SubmittedAt, wantSecond)
	}
}

func TestApprovalsDedup_BetweenFetchPullRequestsAndFetchReviewThreads(t *testing.T) {
	var approvalsHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(mrDetailFixture(1, "base123"))
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		approvalsHits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 1,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if obs[0].Review.Decision != "approved" {
		t.Errorf("FetchPullRequests Review.Decision = %q, want %q", obs[0].Review.Decision, "approved")
	}
	hitsAfterFetchPR := approvalsHits.Load()
	if hitsAfterFetchPR != 1 {
		t.Fatalf("approvals endpoint hit %d times after FetchPullRequests, want 1", hitsAfterFetchPR)
	}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReviewThreads: %v", err)
	}
	if got := approvalsHits.Load(); got != 1 {
		t.Errorf("approvals endpoint hit %d times after FetchReviewThreads, want 1 (served from cache)", got)
	}
	if review.Decision != "approved" {
		t.Errorf("FetchReviewThreads Decision = %q, want %q (cached approvals must preserve decision)", review.Decision, "approved")
	}
	if len(review.Reviews) != 1 {
		t.Fatalf("FetchReviewThreads Reviews = %d, want 1 (cached approvals must preserve summaries)", len(review.Reviews))
	}
	if review.Reviews[0].Author != "reviewer1" {
		t.Errorf("FetchReviewThreads Reviews[0].Author = %q, want %q", review.Reviews[0].Author, "reviewer1")
	}
	if !review.Reviews[0].SubmittedAt.IsZero() {
		t.Errorf("FetchReviewThreads Reviews[0].SubmittedAt = %v, want zero (approved_at absent in fixture)", review.Reviews[0].SubmittedAt)
	}
}

func TestApprovalsDedup_TTLExpiry(t *testing.T) {
	var approvalsHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		approvalsHits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 1,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	clock := &syntheticClock{t: time.Now()}
	p.cache.now = clock.now

	if _, err := p.FetchReviewThreads(context.Background(), ref); err != nil {
		t.Fatalf("first FetchReviewThreads: %v", err)
	}
	if got := approvalsHits.Load(); got != 1 {
		t.Fatalf("approvals endpoint hit %d times after first call, want 1", got)
	}

	if _, err := p.FetchReviewThreads(context.Background(), ref); err != nil {
		t.Fatalf("second FetchReviewThreads (within TTL): %v", err)
	}
	if got := approvalsHits.Load(); got != 1 {
		t.Errorf("approvals endpoint hit %d times after second call (within TTL), want 1 (cache hit)", got)
	}

	clock.advance(approvalsCacheTTL + time.Second)
	if _, err := p.FetchReviewThreads(context.Background(), ref); err != nil {
		t.Fatalf("third FetchReviewThreads (after TTL): %v", err)
	}
	if got := approvalsHits.Load(); got != 2 {
		t.Errorf("approvals endpoint hit %d times after TTL expiry, want 2 (cache expired, must refetch)", got)
	}
}

func TestFetchReviewThreads_StableReviewIDs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 2,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
				{"user": map[string]any{"username": "reviewer2"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Reviews) != 2 {
		t.Fatalf("Reviews = %d, want 2", len(review.Reviews))
	}
	seen := map[string]bool{}
	for _, r := range review.Reviews {
		if r.ID == "" {
			t.Error("review summary has empty ID — multiple approvers would overwrite each other")
		}
		if seen[r.ID] {
			t.Errorf("duplicate review ID %q — approvers collide in persistence", r.ID)
		}
		seen[r.ID] = true
		if r.Author == "reviewer1" && r.ID != "approval:reviewer1" {
			t.Errorf("reviewer1 ID = %q, want %q", r.ID, "approval:reviewer1")
		}
		if r.Author == "reviewer2" && r.ID != "approval:reviewer2" {
			t.Errorf("reviewer2 ID = %q, want %q", r.ID, "approval:reviewer2")
		}
	}
}

func TestFetchApprovalDecision_TrustsApprovedField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 2,
			"approved_by":        []any{},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	decision, err := p.fetchApprovalDecision(context.Background(), repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "approved" {
		t.Errorf("decision = %q, want %q (must trust approved field, not len(approved_by))", decision, "approved")
	}
}

func TestFetchApprovalDecision_NotApproved(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           false,
			"approvals_required": 2,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	decision, err := p.fetchApprovalDecision(context.Background(), repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "review_required" {
		t.Errorf("decision = %q, want %q", decision, "review_required")
	}
}

func TestApprovalDecision(t *testing.T) {
	tests := []struct {
		name              string
		approved          bool
		approvalsRequired int
		approvedByCount   int
		want              domain.ReviewDecision
	}{
		{
			name:              "vacuous approval: approved, no rules, no approvers",
			approved:          true,
			approvalsRequired: 0,
			approvedByCount:   0,
			want:              domain.ReviewNone,
		},
		{
			name:              "approved with approvals_required > 0",
			approved:          true,
			approvalsRequired: 2,
			approvedByCount:   0,
			want:              domain.ReviewApproved,
		},
		{
			name:              "approved with approvers present",
			approved:          true,
			approvalsRequired: 0,
			approvedByCount:   1,
			want:              domain.ReviewApproved,
		},
		{
			name:              "not approved, approvals_required > 0",
			approved:          false,
			approvalsRequired: 2,
			approvedByCount:   0,
			want:              domain.ReviewRequired,
		},
		{
			name:              "not approved, no rules, no approvers",
			approved:          false,
			approvalsRequired: 0,
			approvedByCount:   0,
			want:              domain.ReviewNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := restApprovals{
				Approved:          tt.approved,
				ApprovalsRequired: tt.approvalsRequired,
			}
			for i := 0; i < tt.approvedByCount; i++ {
				a.ApprovedBy = append(a.ApprovedBy, struct {
					User struct {
						Username string `json:"username"`
					} `json:"user"`
					ApprovedAt *time.Time `json:"approved_at"`
				}{
					User: struct {
						Username string `json:"username"`
					}{
						Username: "reviewer1",
					},
				})
			}
			got := approvalDecision(a)
			if got != tt.want {
				t.Errorf("approvalDecision(%+v) = %q, want %q", a, got, tt.want)
			}
		})
	}
}

func TestCommitChecksGuard(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	res1, err := p.CommitChecksGuard(context.Background(), repo, "abc123", "")
	if err != nil {
		t.Fatal(err)
	}
	if res1.ETag == "" {
		t.Error("expected non-empty ETag")
	}
	if res1.NotModified {
		t.Error("expected not NotModified on first call")
	}

	res2, err := p.CommitChecksGuard(context.Background(), repo, "abc123", res1.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.NotModified {
		t.Error("expected NotModified on second call with same data")
	}
}

func TestCommitChecksGuard_ThenFetchPullRequests_Pipelines304Reused(t *testing.T) {
	var pipelines200 atomic.Int32
	var pipelinesTotal atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		pipelinesTotal.Add(1)
		if inm := r.Header.Get("If-None-Match"); inm == `"pipelines-v1"` {
			w.Header().Set("ETag", `"pipelines-v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"pipelines-v1"`)
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
		pipelines200.Add(1)
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "can_be_merged", "author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	if _, err := p.CommitChecksGuard(context.Background(), repo, "abc123", ""); err != nil {
		t.Fatalf("CommitChecksGuard: %v", err)
	}

	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected 1 fetched observation, got %+v", obs)
	}
	if obs[0].CI.Summary != "passing" {
		t.Errorf("CI.Summary = %q, want %q", obs[0].CI.Summary, "passing")
	}

	if got := pipelines200.Load(); got != 1 {
		t.Errorf("pipelines 200 count = %d, want 1 (second call should be a 304 served by ETag cache)", got)
	}
	if got := pipelinesTotal.Load(); got != 2 {
		t.Errorf("pipelines total requests = %d, want 2 (one 200 + one 304)", got)
	}
}

func TestMergeabilityFromMR(t *testing.T) {
	tests := []struct {
		name           string
		detailedStatus string
		legacyStatus   string
		ciState        string
		review         string
		draft          bool
		wantState      string
		wantConflict   bool
		wantBehind     bool
		wantBlockers   []string
	}{
		{"mergeable", "mergeable", "", "passing", "approved", false, "mergeable", false, false, nil},
		{"mergeable+failing-ci", "mergeable", "", "failing", "approved", false, "blocked", false, false, []string{"ci_failing"}},
		{"mergeable+review-required", "mergeable", "", "passing", "review_required", false, "blocked", false, false, []string{"review_required"}},
		{"mergeable+draft", "mergeable", "", "passing", "approved", true, "blocked", false, false, []string{"draft"}},
		{"conflict", "conflict", "", "passing", "approved", false, "conflicting", true, false, []string{"conflicts"}},
		{"checking", "checking", "", "passing", "approved", false, "unknown", false, false, nil},
		{"preparing", "preparing", "", "passing", "approved", false, "unknown", false, false, nil},
		{"unchecked", "unchecked", "", "passing", "approved", false, "unknown", false, false, nil},
		{"need_rebase", "need_rebase", "", "passing", "approved", false, "blocked", false, true, []string{"behind_base"}},
		{"requested_changes", "requested_changes", "", "passing", "approved", false, "blocked", false, false, []string{"changes_requested"}},
		{"not_approved", "not_approved", "", "passing", "none", false, "blocked", false, false, []string{"review_required"}},
		{"ci_must_pass", "ci_must_pass", "", "passing", "approved", false, "blocked", false, false, []string{"ci_failing"}},
		{"ci_still_running", "ci_still_running", "", "passing", "approved", false, "blocked", false, false, []string{"ci_failing"}},
		{"discussions_not_resolved", "discussions_not_resolved", "", "passing", "approved", false, "blocked", false, false, []string{"discussions_unresolved"}},
		{"draft_status", "draft_status", "", "passing", "approved", false, "blocked", false, false, []string{"draft"}},
		{"not_open", "not_open", "", "passing", "approved", false, "blocked", false, false, []string{"blocked_by_provider"}},
		{"merge_request_blocked", "merge_request_blocked", "", "passing", "approved", false, "blocked", false, false, []string{"blocked_by_provider"}},

		{"legacy-can_be_merged", "", "can_be_merged", "passing", "approved", false, "mergeable", false, false, nil},
		{"legacy-cannot_be_merged", "", "cannot_be_merged", "passing", "approved", false, "conflicting", true, false, []string{"conflicts"}},
		{"legacy-cannot_be_merged_recheck", "", "cannot_be_merged_recheck", "passing", "approved", false, "conflicting", true, false, []string{"conflicts"}},
		{"legacy-checking", "", "checking", "passing", "approved", false, "unknown", false, false, nil},
		{"legacy-unchecked", "", "unchecked", "passing", "approved", false, "unknown", false, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := &restMR{MergeStatus: tt.legacyStatus, DetailedMergeStatus: tt.detailedStatus, Draft: tt.draft}
			got := mergeabilityFromMR(mr, tt.ciState, tt.review)
			if got.State != tt.wantState {
				t.Errorf("State = %q, want %q", got.State, tt.wantState)
			}
			if got.Conflict != tt.wantConflict {
				t.Errorf("Conflict = %v, want %v", got.Conflict, tt.wantConflict)
			}
			if got.BehindBase != tt.wantBehind {
				t.Errorf("BehindBase = %v, want %v", got.BehindBase, tt.wantBehind)
			}
			if tt.wantBlockers != nil {
				if len(got.Blockers) != len(tt.wantBlockers) {
					t.Fatalf("Blockers = %v, want %v", got.Blockers, tt.wantBlockers)
				}
				for i, b := range tt.wantBlockers {
					if got.Blockers[i] != b {
						t.Errorf("Blockers[%d] = %q, want %q", i, got.Blockers[i], b)
					}
				}
			}
		})
	}
}

func TestFetchPullRequests_DetailedMergeStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status":          "can_be_merged",
			"detailed_merge_status": "mergeable",
			"author":                map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "success", "sha": "abc123"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": true, "approvals_required": 1, "approved_by": []map[string]any{{"user": map[string]any{"username": "reviewer1"}}}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Mergeability.State != "mergeable" {
		t.Errorf("Mergeability.State = %q, want %q (detailed_merge_status=mergeable must map to mergeable, not blocked)", obs[0].Mergeability.State, "mergeable")
	}
	if obs[0].Mergeability.Conflict {
		t.Error("Mergeability.Conflict = true, want false for mergeable MR")
	}
}

func TestNormalizeMRState(t *testing.T) {
	tests := []struct {
		state string
		draft bool
		want  string
	}{
		{"opened", false, "open"},
		{"opened", true, "draft"},
		{"merged", false, "merged"},
		{"closed", false, "closed"},
		{"locked", false, "closed"},
	}
	for _, tt := range tests {
		t.Run(tt.state+"/"+strconv.FormatBool(tt.draft), func(t *testing.T) {
			got := normalizeMRState(tt.state, tt.draft)
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPipelineStatusToCI(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"success", "passing"},
		{"failed", "failing"},
		{"canceled", "unknown"},
		{"running", "pending"},
		{"pending", "pending"},
		{"created", "pending"},
		{"skipped", "unknown"},
		{"manual", "unknown"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := pipelineStatusToCI(tt.status)
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsBotAuthor(t *testing.T) {
	tests := []struct {
		username string
		want     bool
	}{
		{"gitlab-bot", true},
		{"ghost", true},
		{"dependabot[bot]", true},
		{"project_123_bot", true},
		{"alice", false},
		{"robotics-team", false},
	}
	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			got := isBotAuthor(tt.username)
			if got != tt.want {
				t.Errorf("isBotAuthor(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}

func TestSCMCredentialsAvailable(t *testing.T) {
	p, _ := NewProvider(ProviderOptions{
		Client: NewClient(ClientOptions{Token: StaticTokenSource("tok")}),
	})
	avail, err := p.SCMCredentialsAvailable(context.Background())
	if err != nil || !avail {
		t.Errorf("want (true, nil), got (%v, %v)", avail, err)
	}

	pEmpty, _ := NewProvider(ProviderOptions{
		Client: NewClient(ClientOptions{Token: StaticTokenSource("")}),
	})
	avail, err = pEmpty.SCMCredentialsAvailable(context.Background())
	if err != nil || avail {
		t.Errorf("want (false, nil), got (%v, %v)", avail, err)
	}
}

func TestSCMCredentialsAvailable_HostTokens(t *testing.T) {
	pHostOnly, _ := NewProvider(ProviderOptions{
		Client:             NewClient(ClientOptions{Token: StaticTokenSource("")}),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.internal"},
		HostTokens: map[string]TokenSource{
			"gitlab.internal": StaticTokenSource("host-token-123"),
		},
	})
	avail, err := pHostOnly.SCMCredentialsAvailable(context.Background())
	if err != nil || !avail {
		t.Errorf("no default token + valid hostToken: want (true, nil), got (%v, %v)", avail, err)
	}

	pNoTokens, _ := NewProvider(ProviderOptions{
		Client:             NewClient(ClientOptions{Token: StaticTokenSource("")}),
		SkipTokenPreflight: true,
	})
	avail, err = pNoTokens.SCMCredentialsAvailable(context.Background())
	if err != nil || avail {
		t.Errorf("no default token, no hostTokens: want (false, nil), got (%v, %v)", avail, err)
	}

	pDefaultOnly, _ := NewProvider(ProviderOptions{
		Client: NewClient(ClientOptions{Token: StaticTokenSource("default-token")}),
	})
	avail, err = pDefaultOnly.SCMCredentialsAvailable(context.Background())
	if err != nil || !avail {
		t.Errorf("valid default token, no hostTokens: want (true, nil), got (%v, %v)", avail, err)
	}

	errSrc := &errorTokenSource{err: errors.New("glab command failed")}
	pHostErr, _ := NewProvider(ProviderOptions{
		Client:             NewClient(ClientOptions{Token: StaticTokenSource("")}),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.internal"},
		HostTokens: map[string]TokenSource{
			"gitlab.internal": errSrc,
		},
	})
	avail, err = pHostErr.SCMCredentialsAvailable(context.Background())
	if avail {
		t.Errorf("no default token + hostToken error: want avail=false, got true")
	}
	if err == nil {
		t.Errorf("no default token + hostToken error: want non-nil error, got nil")
	}
}

type errorTokenSource struct {
	err error
}

func (s *errorTokenSource) Token(context.Context) (string, error) {
	return "", s.err
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{404, ErrNotFound},
		{401, ErrAuthFailed},
		{403, ErrAuthFailed},
		{429, ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Status: http.StatusText(tt.status)}
			got := classifyError(resp, nil)
			if !strings.Contains(got.Error(), tt.want.Error()) {
				t.Errorf("classifyError(%d) = %v, want to contain %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestClassifyError_RateLimitRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     http.StatusText(http.StatusTooManyRequests),
		Header: http.Header{
			"Retry-After": []string{"120"},
		},
	}
	err := classifyError(resp, nil)
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("classifyError(429+Retry-After) = %T, want *RateLimitError", err)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("errors.Is(err, ErrRateLimited) = false, want true")
	}
	if rle.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want 2m0s", rle.RetryAfter)
	}
}

func TestClassifyError_RateLimitResetHeader(t *testing.T) {
	resetEpoch := time.Now().Add(5 * time.Minute).Unix()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     http.StatusText(http.StatusTooManyRequests),
		Header:     http.Header{},
	}
	resp.Header.Set("RateLimit-Reset", strconv.FormatInt(resetEpoch, 10))
	err := classifyError(resp, nil)
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("classifyError(429+RateLimit-Reset) = %T, want *RateLimitError", err)
	}
	if rle.ResetAt.IsZero() {
		t.Error("ResetAt = zero, want parsed from RateLimit-Reset header")
	}
	if diff := time.Until(rle.ResetAt); diff < 4*time.Minute || diff > 6*time.Minute {
		t.Errorf("ResetAt is %v from now, want ~5m", diff)
	}
}

func TestNewClient_DefaultHTTPTimeout(t *testing.T) {
	c := NewClient(ClientOptions{Token: StaticTokenSource("tok")})
	if c.http == nil {
		t.Fatal("http client = nil")
	}
	if c.http.Timeout <= 0 {
		t.Errorf("http client Timeout = %v, want finite (>0)", c.http.Timeout)
	}
}

func TestFetchPullRequests_SelfManagedRESTBase(t *testing.T) {
	var seenHost string
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		seenPath = r.URL.Path
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge_requests/1"):
			json.NewEncoder(w).Encode(map[string]any{
				"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
				"web_url":       "https://gitlab.mycompany.com/eng/team/-/merge_requests/1",
				"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
				"merge_status": "mergeable", "detailed_merge_status": "mergeable",
				"author": map[string]any{"username": "alice"},
			})
		case strings.HasSuffix(r.URL.Path, "/pipelines"):
			json.NewEncoder(w).Encode([]map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/approvals"):
			json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
		default:
			json.NewEncoder(w).Encode([]any{})
		}
	}))
	t.Cleanup(srv.Close)

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("test-token"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.mycompany.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.hostClientCfg = ClientOptions{
		Token: StaticTokenSource("test-token"),
		HTTPClient: &http.Client{
			Transport: &rewriteTransport{target: srv.URL},
		},
	}
	p.client = NewClient(ClientOptions{
		Token:      StaticTokenSource("test-token"),
		RESTBase:   srv.URL + "/api/v4",
		HTTPClient: &http.Client{Transport: &rewriteTransport{target: srv.URL}},
	})

	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.mycompany.com", Owner: "eng/team", Name: "widget", Repo: "eng/team/widget"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.mycompany.com/eng/team/-/merge_requests/1"}

	if _, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref}); err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if seenHost == "" {
		t.Fatal("no request was made to the self-managed host")
	}
	if seenHost == "gitlab.com" {
		t.Errorf("request went to gitlab.com (host=%q), want self-managed test server — REST base not derived from host", seenHost)
	}
	if !strings.Contains(seenPath, "/projects/") {
		t.Errorf("unexpected path: %q", seenPath)
	}
}

type rewriteTransport struct {
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	u, err := url.Parse(t.target + req2.URL.Path)
	if err != nil {
		return nil, err
	}
	u.RawQuery = req2.URL.RawQuery
	req2.URL = u
	return http.DefaultTransport.RoundTrip(req2)
}

func TestFetchPullRequests_PipelineJobsPagination(t *testing.T) {
	page := 1
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "mergeable", "detailed_merge_status": "mergeable",
			"author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "failed", "sha": "abc123"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		if page == 1 {
			jobs := make([]map[string]any, pipelineJobsPageSize)
			for i := range jobs {
				jobs[i] = map[string]any{
					"id": 200 + i, "name": fmt.Sprintf("job-%d", i), "status": "success",
					"web_url": fmt.Sprintf("https://gitlab.com/jobs/%d", 200+i),
				}
			}
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/api/v4/projects/myorg%%2Fmyrepo/pipelines/100/jobs?per_page=%d&page=2>; rel="next"`, r.Host, pipelineJobsPageSize))
			json.NewEncoder(w).Encode(jobs)
			page = 2
			return
		}
		jobs := make([]map[string]any, 50)
		for i := range jobs {
			jobs[i] = map[string]any{
				"id": 300 + i, "name": fmt.Sprintf("job-extra-%d", i), "status": "failed",
				"web_url": fmt.Sprintf("https://gitlab.com/jobs/%d", 300+i),
			}
		}
		json.NewEncoder(w).Encode(jobs)
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	totalWant := pipelineJobsPageSize + 50
	if len(obs[0].CI.Checks) != totalWant {
		t.Errorf("CI.Checks = %d, want %d (pagination must follow Link rel=next)", len(obs[0].CI.Checks), totalWant)
	}
	if len(obs[0].CI.FailedChecks) != 50 {
		t.Errorf("CI.FailedChecks = %d, want 50 (all from page 2)", len(obs[0].CI.FailedChecks))
	}
}

func TestFetchPullRequests_CanceledPipeline(t *testing.T) {
	cases := []struct {
		name       string
		jobs       []map[string]any
		wantCI     domain.CIState
		wantFailed []string
	}{
		{
			name:   "only canceled jobs is not a failure",
			jobs:   []map[string]any{{"id": 1, "name": "lint", "status": "canceled"}},
			wantCI: domain.CIUnknown,
		},
		{
			name: "a required job that already failed stays failing",
			jobs: []map[string]any{
				{"id": 1, "name": "lint", "status": "canceled"},
				{"id": 2, "name": "build", "status": "failed"},
			},
			wantCI:     domain.CIFailing,
			wantFailed: []string{"build"},
		},
		{
			name:   "an optional failed job does not make it failing",
			jobs:   []map[string]any{{"id": 2, "name": "build", "status": "failed", "allow_failure": true}},
			wantCI: domain.CIUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{
					"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
					"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
					"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
					"merge_status": "mergeable", "detailed_merge_status": "mergeable",
					"author": map[string]any{"username": "alice"},
				})
			})
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "canceled", "sha": "abc123"}})
			})
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(tc.jobs)
			})
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
			})
			_, p := testServer(t, mux)
			repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
			ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

			obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
			if err != nil {
				t.Fatal(err)
			}
			if obs[0].CI.Summary != string(tc.wantCI) {
				t.Fatalf("CI summary = %q, want %q", obs[0].CI.Summary, tc.wantCI)
			}
			var failed []string
			for _, ch := range obs[0].CI.FailedChecks {
				failed = append(failed, ch.Name)
			}
			if !slices.Equal(failed, tc.wantFailed) {
				t.Fatalf("failed checks = %v, want %v", failed, tc.wantFailed)
			}
		})
	}
}

func TestFetchReviewThreads_DiscussionsPagination(t *testing.T) {
	page := 1
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		if page == 1 {
			discs := make([]map[string]any, reviewDiscussionPageSize)
			for i := range discs {
				discs[i] = map[string]any{
					"id":              fmt.Sprintf("disc-%d", i),
					"individual_note": false,
					"notes": []map[string]any{{
						"id": 100 + i, "body": "comment", "system": false,
						"resolvable": true, "resolved": true,
						"author": map[string]any{"username": "reviewer1"},
					}},
				}
			}
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/api/v4/projects/myorg%%2Fmyrepo/merge_requests/1/discussions?per_page=%d&page=2>; rel="next"`, r.Host, reviewDiscussionPageSize))
			json.NewEncoder(w).Encode(discs)
			page = 2
			return
		}
		discs := make([]map[string]any, 30)
		for i := range discs {
			discs[i] = map[string]any{
				"id":              fmt.Sprintf("disc-extra-%d", i),
				"individual_note": false,
				"notes": []map[string]any{{
					"id": 200 + i, "body": "extra comment", "system": false,
					"resolvable": true, "resolved": false,
					"author": map[string]any{"username": "reviewer2"},
				}},
			}
		}
		json.NewEncoder(w).Encode(discs)
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	totalWant := reviewDiscussionPageSize + 30
	if len(review.Threads) != totalWant {
		t.Errorf("Threads = %d, want %d (pagination must follow Link rel=next)", len(review.Threads), totalWant)
	}
	if review.Partial {
		t.Error("Partial = true, want false (all discussions fetched, no truncation)")
	}
}

func TestFetchReviewThreads_DiscussionsTruncated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		discs := make([]map[string]any, reviewDiscussionPageSize)
		for i := range discs {
			discs[i] = map[string]any{
				"id":              fmt.Sprintf("disc-%s-%d", r.URL.Query().Get("page"), i),
				"individual_note": false,
				"notes": []map[string]any{{
					"id": 100 + i, "body": "comment", "system": false,
					"resolvable": true, "resolved": true,
					"author": map[string]any{"username": "reviewer1"},
				}},
			}
		}
		w.Header().Set("Link", fmt.Sprintf(`<http://%s/api/v4/projects/myorg%%2Fmyrepo/merge_requests/1/discussions?per_page=%d&page=next>; rel="next"`, r.Host, reviewDiscussionPageSize))
		json.NewEncoder(w).Encode(discs)
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Threads) != maxPaginationPages*reviewDiscussionPageSize {
		t.Errorf("Threads = %d, want %d (10-page cap × page size)", len(review.Threads), maxPaginationPages*reviewDiscussionPageSize)
	}
	if !review.Partial {
		t.Error("Partial = false, want true (pagination cap hit — response is truncated)")
	}
}

func TestFetchPullRequests_TransientFailureReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"500 Internal Server Error"}`)
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err == nil {
		t.Fatal("FetchPullRequests: error = nil, want non-nil error for transient MR detail failure")
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	if obs[0].Fetched {
		t.Error("expected Fetched=false on failed observation so observer can reject it")
	}
}

func TestFetchPullRequests_PipelineFailureReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "can_be_merged", "author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"message":"502 Bad Gateway"}`)
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	if _, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref}); err == nil {
		t.Fatal("FetchPullRequests: error = nil, want non-nil error for transient pipeline failure")
	}
}

func TestFetchPullRequests_ApprovalFailureReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "can_be_merged", "author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "success", "sha": "abc123"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message":"503 Service Unavailable"}`)
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	if _, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref}); err == nil {
		t.Fatal("FetchPullRequests: error = nil, want non-nil error for transient approvals failure")
	}
}

func TestListPRsByRepo_UpdatedAfterAndStateAll(t *testing.T) {
	var seenState, seenUpdatedAfter string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		seenState = r.URL.Query().Get("state")
		seenUpdatedAfter = r.URL.Query().Get("updated_after")
		json.NewEncoder(w).Encode([]map[string]any{})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	cursor := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	if _, err := p.ListPRsByRepo(context.Background(), repo, cursor); err != nil {
		t.Fatal(err)
	}
	if seenState != "all" {
		t.Errorf("state query param = %q, want %q (state=all for closed/merged MR discovery)", seenState, "all")
	}
	if seenUpdatedAfter == "" {
		t.Error("updated_after query param is empty, want RFC3339 timestamp")
	}
	parsed, err := time.Parse(time.RFC3339, seenUpdatedAfter)
	if err != nil {
		t.Fatalf("updated_after %q is not valid RFC3339: %v", seenUpdatedAfter, err)
	}
	if !parsed.Equal(cursor) {
		t.Errorf("updated_after = %v, want %v", parsed, cursor)
	}
}

func TestListPRsByRepo_FirstPollFullListing(t *testing.T) {
	var seenUpdatedAfter string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		seenUpdatedAfter = r.URL.Query().Get("updated_after")
		json.NewEncoder(w).Encode([]map[string]any{})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if seenUpdatedAfter != "" {
		t.Errorf("updated_after = %q, want empty (first poll = full listing)", seenUpdatedAfter)
	}
}

func TestFetchPullRequests_ForkMR_ResolvesSourceProjectHeadRepo(t *testing.T) {
	var sourceProjectHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid":               1,
			"title":             "Fork contribution",
			"state":             "opened",
			"draft":             false,
			"web_url":           "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch":     "fix-bug",
			"target_branch":     "main",
			"sha":               "abc123",
			"merge_status":      "can_be_merged",
			"author":            map[string]any{"username": "alice"},
			"source_project_id": 99,
			"target_project_id": 10,
			"diff_refs":         map[string]any{"base_sha": "base123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/99", func(w http.ResponseWriter, r *http.Request) {
		sourceProjectHits++
		json.NewEncoder(w).Encode(map[string]any{
			"id":                  99,
			"path_with_namespace": "contributor/myrepo-fork",
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: unexpected error: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	o := obs[0]
	if !o.Fetched {
		t.Fatal("expected Fetched=true for a fork MR with a resolvable source project")
	}
	if got, want := o.PR.HeadRepo, "contributor/myrepo-fork"; got != want {
		t.Errorf("PR.HeadRepo = %q, want %q (source project path_with_namespace)", got, want)
	}
	if sourceProjectHits != 1 {
		t.Errorf("source project endpoint hits = %d, want 1", sourceProjectHits)
	}
}

func TestFetchPullRequests_NonForkMR_DoesNotFetchSourceProject(t *testing.T) {
	var sourceProjectHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid":               1,
			"title":             "Same-repo MR",
			"state":             "opened",
			"draft":             false,
			"web_url":           "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch":     "fix-bug",
			"target_branch":     "main",
			"sha":               "abc123",
			"merge_status":      "can_be_merged",
			"author":            map[string]any{"username": "alice"},
			"source_project_id": 10,
			"target_project_id": 10,
			"diff_refs":         map[string]any{"base_sha": "base123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	mux.HandleFunc("/api/v4/projects/10", func(w http.ResponseWriter, r *http.Request) {
		sourceProjectHits++
		t.Errorf("source project endpoint hit for non-fork MR; should not be fetched")
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: unexpected error: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got, want := obs[0].PR.HeadRepo, "myorg/myrepo"; got != want {
		t.Errorf("PR.HeadRepo = %q, want %q (target repo for non-fork MR)", got, want)
	}
	if sourceProjectHits != 0 {
		t.Errorf("source project endpoint hits = %d, want 0 (non-fork MR must not trigger a fetch)", sourceProjectHits)
	}
}

func TestFetchPullRequests_ForkMR_SourceProjectFetchFails_FailClosed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid":               1,
			"title":             "Fork with unresolvable source",
			"state":             "opened",
			"draft":             false,
			"web_url":           "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch":     "fix-bug",
			"target_branch":     "main",
			"sha":               "abc123",
			"merge_status":      "can_be_merged",
			"author":            map[string]any{"username": "alice"},
			"source_project_id": 99,
			"target_project_id": 10,
			"diff_refs":         map[string]any{"base_sha": "base123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/99", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Project Not Found"}`)
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err == nil {
		t.Fatal("FetchPullRequests: error = nil, want non-nil for failed fork-MR source-project fetch")
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	o := obs[0]
	if o.Fetched {
		t.Error("expected Fetched=false (fail closed) when source-project fetch fails")
	}
	if o.Error == nil {
		t.Error("expected obs.Error set on failed fork-MR observation")
	}
	if o.PR.HeadRepo != "" {
		t.Errorf("PR.HeadRepo = %q, want empty (must not fabricate head repo on fetch failure)", o.PR.HeadRepo)
	}
}

func TestFetchPullRequests_ForkMR_SourceProjectCachedWithinTTL(t *testing.T) {
	var sourceProjectHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid":               1,
			"title":             "Fork contribution",
			"state":             "opened",
			"draft":             false,
			"web_url":           "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch":     "fix-bug",
			"target_branch":     "main",
			"sha":               "abc123",
			"merge_status":      "can_be_merged",
			"author":            map[string]any{"username": "alice"},
			"source_project_id": 99,
			"target_project_id": 10,
			"diff_refs":         map[string]any{"base_sha": "base123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/99", func(w http.ResponseWriter, r *http.Request) {
		sourceProjectHits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"id":                  99,
			"path_with_namespace": "contributor/myrepo-fork",
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	clock := &syntheticClock{t: time.Now()}
	p.cache.now = clock.now

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("first FetchPullRequests: unexpected error: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("first call: expected one fetched observation, got %+v", obs)
	}
	if got, want := obs[0].PR.HeadRepo, "contributor/myrepo-fork"; got != want {
		t.Fatalf("first call: PR.HeadRepo = %q, want %q", got, want)
	}
	if got := sourceProjectHits.Load(); got != 1 {
		t.Fatalf("source-project endpoint hits after first call = %d, want 1", got)
	}

	obs2, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("second FetchPullRequests (within TTL): unexpected error: %v", err)
	}
	if len(obs2) != 1 || !obs2[0].Fetched {
		t.Fatalf("second call: expected one fetched observation, got %+v", obs2)
	}
	if got, want := obs2[0].PR.HeadRepo, "contributor/myrepo-fork"; got != want {
		t.Errorf("second call: PR.HeadRepo = %q, want %q (cached path on cache hit)", got, want)
	}
	if got := sourceProjectHits.Load(); got != 1 {
		t.Errorf("source-project endpoint hits after second call (within TTL) = %d, want 1 (cache hit)", got)
	}
}

func TestFetchPullRequests_ForkMR_SourceProjectCacheExpiresAfterTTL(t *testing.T) {
	var sourceProjectHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid":               1,
			"title":             "Fork contribution",
			"state":             "opened",
			"draft":             false,
			"web_url":           "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch":     "fix-bug",
			"target_branch":     "main",
			"sha":               "abc123",
			"merge_status":      "can_be_merged",
			"author":            map[string]any{"username": "alice"},
			"source_project_id": 99,
			"target_project_id": 10,
			"diff_refs":         map[string]any{"base_sha": "base123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/99", func(w http.ResponseWriter, r *http.Request) {
		sourceProjectHits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"id":                  99,
			"path_with_namespace": "contributor/myrepo-fork",
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	clock := &syntheticClock{t: time.Now()}
	p.cache.now = clock.now

	if _, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref}); err != nil {
		t.Fatalf("first FetchPullRequests: %v", err)
	}
	if got := sourceProjectHits.Load(); got != 1 {
		t.Fatalf("source-project endpoint hits after first call = %d, want 1", got)
	}

	clock.advance(forkProjectCacheTTL + time.Second)
	if _, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref}); err != nil {
		t.Fatalf("second FetchPullRequests (after TTL): %v", err)
	}
	if got := sourceProjectHits.Load(); got != 2 {
		t.Errorf("source-project endpoint hits after TTL expiry = %d, want 2 (cache expired, must refetch)", got)
	}
}

func TestFetchPullRequests_ConcurrentCacheAccess_RaceSafe(t *testing.T) {
	var sourceProjectHits atomic.Int32
	var mrDetailHits atomic.Int32
	var approvalsHits atomic.Int32
	var pipelinesHits atomic.Int32

	mux := http.NewServeMux()

	const numRefs = 7
	for i := 1; i <= numRefs; i++ {
		iid := i
		sha := fmt.Sprintf("sha%d", iid)
		mrPath := fmt.Sprintf("/api/v4/projects/myorg%%2Fmyrepo/merge_requests/%d", iid)
		approvalsPath := fmt.Sprintf("/api/v4/projects/myorg%%2Fmyrepo/merge_requests/%d/approvals", iid)
		jobsPath := fmt.Sprintf("/api/v4/projects/myorg%%2Fmyrepo/pipelines/%d/jobs", 100+iid)

		mux.HandleFunc(mrPath, func(w http.ResponseWriter, r *http.Request) {
			mrDetailHits.Add(1)
			json.NewEncoder(w).Encode(map[string]any{
				"iid":               iid,
				"title":             fmt.Sprintf("Fork MR %d", iid),
				"state":             "opened",
				"draft":             false,
				"web_url":           fmt.Sprintf("https://gitlab.com/myorg/myrepo/-/merge_requests/%d", iid),
				"source_branch":     fmt.Sprintf("fix-bug-%d", iid),
				"target_branch":     "main",
				"sha":               sha,
				"merge_status":      "can_be_merged",
				"author":            map[string]any{"username": "alice"},
				"source_project_id": 99,
				"target_project_id": 10,
				"diff_refs":         map[string]any{"base_sha": "base123"},
			})
		})
		mux.HandleFunc(approvalsPath, func(w http.ResponseWriter, r *http.Request) {
			approvalsHits.Add(1)
			json.NewEncoder(w).Encode(map[string]any{
				"approved":           false,
				"approvals_required": 0,
				"approved_by":        []any{},
			})
		})
		mux.HandleFunc(jobsPath, func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 200 + iid, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
			})
		})
	}

	mux.HandleFunc("/api/v4/projects/99", func(w http.ResponseWriter, r *http.Request) {
		sourceProjectHits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"id":                  99,
			"path_with_namespace": "contributor/myrepo-fork",
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		pipelinesHits.Add(1)
		sha := r.URL.Query().Get("sha")
		var pipelineID int
		for i := 1; i <= numRefs; i++ {
			if sha == fmt.Sprintf("sha%d", i) {
				pipelineID = 100 + i
				break
			}
		}
		if pipelineID == 0 {
			t.Errorf("unexpected pipelines query sha=%q", sha)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": pipelineID, "status": "success", "sha": sha},
		})
	})

	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	refs := make([]ports.SCMPRRef, numRefs)
	for i := 1; i <= numRefs; i++ {
		refs[i-1] = ports.SCMPRRef{
			Repo:   repo,
			Number: i,
			URL:    fmt.Sprintf("https://gitlab.com/myorg/myrepo/-/merge_requests/%d", i),
		}
	}

	obs, err := p.FetchPullRequests(context.Background(), refs)
	if err != nil {
		t.Fatalf("FetchPullRequests under concurrent access: unexpected error: %v", err)
	}
	if len(obs) != numRefs {
		t.Fatalf("got %d observations, want %d", len(obs), numRefs)
	}

	for i, o := range obs {
		if !o.Fetched {
			t.Errorf("obs[%d].Fetched = false, want true (cache race may have dropped an entry)", i)
			continue
		}
		if got, want := o.PR.HeadRepo, "contributor/myrepo-fork"; got != want {
			t.Errorf("obs[%d].PR.HeadRepo = %q, want %q (shared fork-project cache entry corrupted by concurrent access)", i, got, want)
		}
		if got, want := o.PR.Number, i+1; got != want {
			t.Errorf("obs[%d].PR.Number = %d, want %d (result ordering broken)", i, got, want)
		}
	}

	if got := sourceProjectHits.Load(); got < 1 || got > int32(fetchConcurrency) {
		t.Errorf("source-project endpoint hits = %d, want in [1, %d] (fork-project cache: at least one fetch, at most one per concurrent goroutine)", got, fetchConcurrency)
	} else {
		t.Logf("source-project endpoint hits = %d (cache stampede within fetchConcurrency=%d is expected; single-flight is not in scope per spec)", got, fetchConcurrency)
	}
	if got, want := mrDetailHits.Load(), int32(numRefs); got != want {
		t.Errorf("MR-detail endpoint hits = %d, want %d (one fetch per ref, cache keyed by iid)", got, want)
	}
	if got, want := approvalsHits.Load(), int32(numRefs); got != want {
		t.Errorf("approvals endpoint hits = %d, want %d (one fetch per ref, cache keyed by iid)", got, want)
	}
	if got, want := pipelinesHits.Load(), int32(numRefs); got != want {
		t.Errorf("pipelines endpoint hits = %d, want %d (one fetch per ref)", got, want)
	}
}

func TestFetchPullRequests_PipelineJobsTruncated_Partial(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "mergeable", "detailed_merge_status": "mergeable",
			"author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "success", "sha": "abc123"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs := make([]map[string]any, pipelineJobsPageSize)
		for i := range jobs {
			jobs[i] = map[string]any{
				"id": 200 + i, "name": fmt.Sprintf("job-%s-%d", r.URL.Query().Get("page"), i),
				"status": "success", "web_url": fmt.Sprintf("https://gitlab.com/jobs/%d", 200+i),
			}
		}
		w.Header().Set("Link", fmt.Sprintf(`<http://%s/api/v4/projects/myorg%%2Fmyrepo/pipelines/100/jobs?per_page=%d&page=next>; rel="next"`, r.Host, pipelineJobsPageSize))
		json.NewEncoder(w).Encode(jobs)
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	o := obs[0]
	if len(o.CI.Checks) != maxPaginationPages*pipelineJobsPageSize {
		t.Errorf("CI.Checks = %d, want %d (10-page cap × page size)", len(o.CI.Checks), maxPaginationPages*pipelineJobsPageSize)
	}
	if !o.CI.Partial {
		t.Error("CI.Partial = false, want true (pagination cap hit — response is truncated)")
	}
}

func TestFetchPullRequests_AllowFailureJobNotActionable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
			"merge_status": "mergeable", "detailed_merge_status": "mergeable",
			"author": map[string]any{"username": "alice"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "success", "sha": "abc123"}})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200", "allow_failure": false},
			{"id": 201, "name": "flakey-e2e", "status": "failed", "web_url": "https://gitlab.com/jobs/201", "allow_failure": true},
			{"id": 202, "name": "required-test", "status": "failed", "web_url": "https://gitlab.com/jobs/202", "allow_failure": false},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	o := obs[0]
	if len(o.CI.Checks) != 3 {
		t.Fatalf("CI.Checks = %d, want 3 (all jobs, including optional failures)", len(o.CI.Checks))
	}
	if len(o.CI.FailedChecks) != 1 {
		t.Fatalf("FailedChecks = %d, want 1 (only the required failure; optional failures excluded)", len(o.CI.FailedChecks))
	}
	if o.CI.FailedChecks[0].Name != "required-test" {
		t.Errorf("FailedChecks[0].Name = %q, want %q (optional allow_failure job must not be actionable)", o.CI.FailedChecks[0].Name, "required-test")
	}
}

func TestFetchPullRequests_DiffStatsNotParsed(t *testing.T) {
	tests := []struct {
		name    string
		mrBody  map[string]any
		wantAdd int
		wantDel int
		wantChg int
	}{
		{
			name: "diff_stats absent",
			mrBody: map[string]any{
				"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
				"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
				"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
				"merge_status": "can_be_merged", "author": map[string]any{"username": "alice"},
			},
			wantAdd: 0, wantDel: 0, wantChg: 0,
		},
		{
			name: "diff_stats present but ignored",
			mrBody: map[string]any{
				"iid": 1, "title": "Fix bug", "state": "opened", "draft": false,
				"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
				"source_branch": "fix-bug", "target_branch": "main", "sha": "abc123",
				"merge_status": "can_be_merged", "author": map[string]any{"username": "alice"},
				"diff_stats": map[string]any{
					"additions": 42,
					"deletions": 7,
					"changes":   3,
				},
			},
			wantAdd: 0, wantDel: 0, wantChg: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(tt.mrBody)
			})
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode([]map[string]any{{"id": 100, "status": "success", "sha": "abc123"}})
			})
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode([]map[string]any{{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"}})
			})
			mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
			})
			_, p := testServer(t, mux)
			repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
			ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

			obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
			if err != nil {
				t.Fatalf("FetchPullRequests: unexpected error: %v", err)
			}
			if len(obs) != 1 || !obs[0].Fetched {
				t.Fatalf("got %d observations, want 1 Fetched=true", len(obs))
			}
			pr := obs[0].PR
			if pr.Additions != tt.wantAdd {
				t.Errorf("PR.Additions = %d, want %d (diff_stats must not be parsed)", pr.Additions, tt.wantAdd)
			}
			if pr.Deletions != tt.wantDel {
				t.Errorf("PR.Deletions = %d, want %d (diff_stats must not be parsed)", pr.Deletions, tt.wantDel)
			}
			if pr.ChangedFiles != tt.wantChg {
				t.Errorf("PR.ChangedFiles = %d, want %d (diff_stats must not be parsed)", pr.ChangedFiles, tt.wantChg)
			}
		})
	}
}

func mrDetailFixture(iid int, baseSHA string) map[string]any {
	return map[string]any{
		"iid":               iid,
		"title":             "Fix bug",
		"state":             "opened",
		"draft":             false,
		"web_url":           "https://gitlab.com/myorg/myrepo/-/merge_requests/" + strconv.Itoa(iid),
		"source_branch":     "fix-bug",
		"target_branch":     "main",
		"sha":               "abc123",
		"merge_status":      "can_be_merged",
		"author":            map[string]any{"username": "alice"},
		"source_project_id": 10,
		"target_project_id": 10,
		"diff_refs":         map[string]any{"base_sha": baseSHA, "head_sha": "abc123", "start_sha": "parent789"},
	}
}

func registerMRSupportingEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 100, "status": "success", "sha": "abc123"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/100/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 200, "name": "build", "status": "success", "web_url": "https://gitlab.com/jobs/200"},
		})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"approved": true, "approvals_required": 1, "approved_by": []any{}})
	})
}

func TestFetchPullRequests_ReusesMRDetailFromListCache(t *testing.T) {
	var mrDetailHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{mrDetailFixture(1, "base123")})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		mrDetailHits.Add(1)
		json.NewEncoder(w).Encode(mrDetailFixture(1, "base123"))
	})
	registerMRSupportingEndpoints(mux)
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo: %v", err)
	}

	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got := mrDetailHits.Load(); got != 0 {
		t.Errorf("MR detail endpoint hits = %d, want 0 (cached restMR from listing must be reused)", got)
	}
	if got, want := obs[0].PR.BaseSHA, "base123"; got != want {
		t.Errorf("PR.BaseSHA = %q, want %q (served from list cache)", got, want)
	}
	if got, want := obs[0].PR.Number, 1; got != want {
		t.Errorf("PR.Number = %d, want %d", got, want)
	}
	if got, want := obs[0].PR.HeadSHA, "abc123"; got != want {
		t.Errorf("PR.HeadSHA = %q, want %q", got, want)
	}
}

func TestFetchPullRequests_ColdCache_FetchesMRDetailViaHTTP(t *testing.T) {
	var mrDetailHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		mrDetailHits.Add(1)
		json.NewEncoder(w).Encode(mrDetailFixture(1, "base123"))
	})
	registerMRSupportingEndpoints(mux)
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got := mrDetailHits.Load(); got != 1 {
		t.Errorf("MR detail endpoint hits = %d, want 1 (cold cache must fall back to HTTP)", got)
	}
	if got, want := obs[0].PR.BaseSHA, "base123"; got != want {
		t.Errorf("PR.BaseSHA = %q, want %q", got, want)
	}
	if got, want := obs[0].PR.Number, 1; got != want {
		t.Errorf("PR.Number = %d, want %d", got, want)
	}
}

func TestFetchPullRequests_MRDetailCacheTTLExpiry(t *testing.T) {
	var mrDetailHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{mrDetailFixture(1, "base123")})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		mrDetailHits.Add(1)
		json.NewEncoder(w).Encode(mrDetailFixture(1, "base123"))
	})
	registerMRSupportingEndpoints(mux)
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	clock := &syntheticClock{t: time.Now()}
	p.cache.now = clock.now

	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo: %v", err)
	}

	clock.advance(mrDetailCacheTTL + time.Second)

	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got := mrDetailHits.Load(); got != 1 {
		t.Errorf("MR detail endpoint hits = %d, want 1 (cache expired, must fall back to HTTP)", got)
	}
	if got, want := obs[0].PR.BaseSHA, "base123"; got != want {
		t.Errorf("PR.BaseSHA = %q, want %q (served from HTTP after expiry)", got, want)
	}
}

func TestFetchPullRequests_ListCacheMissingDiffRefs_FetchesDetail(t *testing.T) {
	var mrDetailHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		listMR := mrDetailFixture(1, "")
		delete(listMR, "diff_refs")
		json.NewEncoder(w).Encode([]map[string]any{listMR})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		mrDetailHits.Add(1)
		json.NewEncoder(w).Encode(mrDetailFixture(1, "base123"))
	})
	registerMRSupportingEndpoints(mux)
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo: %v", err)
	}

	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got := mrDetailHits.Load(); got != 1 {
		t.Errorf("MR detail endpoint hits = %d, want 1 (cached entry lacks diff_refs.base_sha, must fall back to HTTP)", got)
	}
	if got, want := obs[0].PR.BaseSHA, "base123"; got != want {
		t.Errorf("PR.BaseSHA = %q, want %q (populated from detail HTTP response)", got, want)
	}
}

func TestFetchPullRequests_ListCacheWithDiffRefs_SkipsDetail(t *testing.T) {
	var mrDetailHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{mrDetailFixture(1, "base123")})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		mrDetailHits.Add(1)
		json.NewEncoder(w).Encode(mrDetailFixture(1, "base123"))
	})
	registerMRSupportingEndpoints(mux)
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo: %v", err)
	}

	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got := mrDetailHits.Load(); got != 0 {
		t.Errorf("MR detail endpoint hits = %d, want 0 (cached entry has diff_refs.base_sha, cache must be reused)", got)
	}
	if got, want := obs[0].PR.BaseSHA, "base123"; got != want {
		t.Errorf("PR.BaseSHA = %q, want %q (served from list cache)", got, want)
	}
}

func TestRestMR_DiffRefsBaseSHA_FromListResponse(t *testing.T) {
	payload := `[
	  {
	    "iid": 1,
	    "sha": "abc123",
	    "diff_refs": {
	      "base_sha": "base456",
	      "head_sha": "abc123",
	      "start_sha": "parent789"
	    }
	  }
	]`
	var mrs []restMR
	if err := json.Unmarshal([]byte(payload), &mrs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(mrs) != 1 {
		t.Fatalf("got %d MRs, want 1", len(mrs))
	}
	mr := mrs[0]
	if mr.DiffRefs.BaseSHA != "base456" {
		t.Errorf("DiffRefs.BaseSHA = %q, want %q", mr.DiffRefs.BaseSHA, "base456")
	}
	if mr.DiffRefs.HeadSHA != "abc123" {
		t.Errorf("DiffRefs.HeadSHA = %q, want %q", mr.DiffRefs.HeadSHA, "abc123")
	}
	if mr.DiffRefs.StartSHA != "parent789" {
		t.Errorf("DiffRefs.StartSHA = %q, want %q", mr.DiffRefs.StartSHA, "parent789")
	}
}

func TestAuthenticatedIdentityCachesHumanUser(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "alice"})
			return
		}
		http.NotFound(w, r)
	})
	_, p := testServer(t, handler)

	first, err := p.AuthenticatedIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.AuthenticatedIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != (ports.SCMIdentity{Login: "alice", Human: true}) || second != first {
		t.Fatalf("identities = %#v, %#v", first, second)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("GET /user calls = %d, want 1", got)
	}
}

func TestAuthenticatedIdentityClassifiesBot(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "project_42_bot"})
			return
		}
		http.NotFound(w, r)
	})
	_, p := testServer(t, handler)

	got, err := p.AuthenticatedIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != (ports.SCMIdentity{Login: "project_42_bot", Human: false}) {
		t.Fatalf("identity = %#v", got)
	}
}

func TestAuthenticatedIdentityAuthErrorNotCached(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		http.NotFound(w, r)
	})
	_, p := testServer(t, handler)

	_, err := p.AuthenticatedIdentity(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed, got %v", err)
	}

	_, err = p.AuthenticatedIdentity(context.Background())
	if err == nil {
		t.Fatal("expected error on second call, got nil")
	}
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed on second call, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GET /user calls = %d, want 2 (no caching on auth failure)", got)
	}
}

func TestAuthenticatedIdentityForHost_TwoHostsDifferentIdentities(t *testing.T) {
	defaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "alice-dotcom"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(defaultSrv.Close)

	selfManagedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "bob-internal"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(selfManagedSrv.Close)

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("dotcom-token"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.client = NewClient(ClientOptions{
		Token:    StaticTokenSource("dotcom-token"),
		RESTBase: defaultSrv.URL + "/api/v4",
	})
	p.hostClientCfg = ClientOptions{
		Token: StaticTokenSource("internal-token"),
		HTTPClient: &http.Client{
			Transport: &rewriteTransport{target: selfManagedSrv.URL},
		},
	}

	identDefault, err := p.AuthenticatedIdentityForHost(context.Background(), "")
	if err != nil {
		t.Fatalf("AuthenticatedIdentityForHost empty host: %v", err)
	}
	if identDefault.Login != "alice-dotcom" {
		t.Errorf("default host identity = %q, want %q", identDefault.Login, "alice-dotcom")
	}
	if !identDefault.Human {
		t.Errorf("default host identity Human = false, want true")
	}

	identSelf, err := p.AuthenticatedIdentityForHost(context.Background(), "gitlab.internal")
	if err != nil {
		t.Fatalf("AuthenticatedIdentityForHost(gitlab.internal): %v", err)
	}
	if identSelf.Login != "bob-internal" {
		t.Errorf("self-managed host identity = %q, want %q", identSelf.Login, "bob-internal")
	}
	if !identSelf.Human {
		t.Errorf("self-managed host identity Human = false, want true")
	}

	if identDefault.Login == identSelf.Login {
		t.Errorf("both hosts returned the same identity %q — per-host identity not working", identDefault.Login)
	}
}

func TestAuthenticatedIdentityForHost_PerHostCaching(t *testing.T) {
	var defaultCalls atomic.Int32
	var selfManagedCalls atomic.Int32

	defaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			defaultCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "alice-dotcom"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(defaultSrv.Close)

	selfManagedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			selfManagedCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "bob-internal"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(selfManagedSrv.Close)

	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("dotcom-token"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.client = NewClient(ClientOptions{
		Token:    StaticTokenSource("dotcom-token"),
		RESTBase: defaultSrv.URL + "/api/v4",
	})
	p.hostClientCfg = ClientOptions{
		Token: StaticTokenSource("internal-token"),
		HTTPClient: &http.Client{
			Transport: &rewriteTransport{target: selfManagedSrv.URL},
		},
	}

	if _, err := p.AuthenticatedIdentityForHost(context.Background(), ""); err != nil {
		t.Fatalf("first default call: %v", err)
	}
	if _, err := p.AuthenticatedIdentityForHost(context.Background(), "gitlab.internal"); err != nil {
		t.Fatalf("first self-managed call: %v", err)
	}
	if got := defaultCalls.Load(); got != 1 {
		t.Fatalf("default /user calls after first round = %d, want 1", got)
	}
	if got := selfManagedCalls.Load(); got != 1 {
		t.Fatalf("self-managed /user calls after first round = %d, want 1", got)
	}

	identDefault2, err := p.AuthenticatedIdentityForHost(context.Background(), "")
	if err != nil {
		t.Fatalf("second default call: %v", err)
	}
	identSelf2, err := p.AuthenticatedIdentityForHost(context.Background(), "gitlab.internal")
	if err != nil {
		t.Fatalf("second self-managed call: %v", err)
	}
	if got := defaultCalls.Load(); got != 1 {
		t.Errorf("default /user calls after second round = %d, want 1 (cached)", got)
	}
	if got := selfManagedCalls.Load(); got != 1 {
		t.Errorf("self-managed /user calls after second round = %d, want 1 (cached)", got)
	}

	if identDefault2.Login != "alice-dotcom" {
		t.Errorf("cached default identity = %q, want %q", identDefault2.Login, "alice-dotcom")
	}
	if identSelf2.Login != "bob-internal" {
		t.Errorf("cached self-managed identity = %q, want %q", identSelf2.Login, "bob-internal")
	}
}

func TestAuthenticatedIdentityDelegatesToForHost(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "alice"})
			return
		}
		http.NotFound(w, r)
	})
	_, p := testServer(t, handler)

	ident1, err := p.AuthenticatedIdentity(context.Background())
	if err != nil {
		t.Fatalf("AuthenticatedIdentity: %v", err)
	}
	ident2, err := p.AuthenticatedIdentityForHost(context.Background(), "")
	if err != nil {
		t.Fatalf("AuthenticatedIdentityForHost empty host: %v", err)
	}
	if ident1 != ident2 {
		t.Errorf("AuthenticatedIdentity = %#v, AuthenticatedIdentityForHost empty host = %#v — must be identical", ident1, ident2)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("GET /user calls = %d, want 1 (AuthenticatedIdentity must share cache with AuthenticatedIdentityForHost)", got)
	}
}

func TestAuthenticatedIdentityForHost_RejectedHost(t *testing.T) {
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"gitlab.internal"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.AuthenticatedIdentityForHost(context.Background(), "gitlab.evil.example")
	if err == nil {
		t.Fatal("expected error for non-allowlisted host, got nil")
	}
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("expected ErrHostNotAllowed, got %v", err)
	}
}

func mrDetailFixtureWithStatus(iid int, baseSHA, detailedMergeStatus string) map[string]any {
	m := mrDetailFixture(iid, baseSHA)
	m["detailed_merge_status"] = detailedMergeStatus
	return m
}

func TestFetchPullRequests_RequestedChangesOverridesReviewDecision(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(mrDetailFixtureWithStatus(1, "base123", "requested_changes"))
	})
	registerMRSupportingEndpoints(mux)
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("expected one fetched observation, got %+v", obs)
	}
	if got, want := obs[0].Review.Decision, string(domain.ReviewChangesRequest); got != want {
		t.Errorf("Review.Decision = %q, want %q (requested_changes must override approvals)", got, want)
	}
	if len(obs[0].Mergeability.Blockers) == 0 || obs[0].Mergeability.Blockers[0] != "changes_requested" {
		t.Errorf("Mergeability.Blockers = %v, want [changes_requested]", obs[0].Mergeability.Blockers)
	}
}

func TestFetchReviewThreads_RequestedChangesOverridesWhenMRDetailCached(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{mrDetailFixtureWithStatus(1, "base123", "requested_changes")})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 2,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
				{"user": map[string]any{"username": "reviewer2"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	if _, err := p.ListPRsByRepo(context.Background(), repo, time.Time{}); err != nil {
		t.Fatalf("ListPRsByRepo: %v", err)
	}

	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReviewThreads: %v", err)
	}
	if got, want := review.Decision, string(domain.ReviewChangesRequest); got != want {
		t.Errorf("Decision = %q, want %q (cached MR detail has requested_changes)", got, want)
	}
}

func TestFetchReviewThreads_FallsBackToApprovalsWhenMRDetailCacheCold(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/discussions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"approved":           true,
			"approvals_required": 2,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "reviewer1"}},
				{"user": map[string]any{"username": "reviewer2"}},
			},
		})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}
	ref := ports.SCMPRRef{Repo: repo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}

	review, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReviewThreads: %v", err)
	}
	if got, want := review.Decision, string(domain.ReviewApproved); got != want {
		t.Errorf("Decision = %q, want %q (cold cache must fall back to approvals)", got, want)
	}
}

func TestListOpenPRsByBranchFiltersOnTheServer(t *testing.T) {
	var query url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"iid": 3, "state": "opened", "web_url": "https://gitlab.com/myorg/myrepo/-/merge_requests/3",
			"source_branch": "fix-bug", "target_branch": "main", "author": map[string]any{"username": "alice"},
			"source_project_id": 10, "target_project_id": 10,
		}, {
			"iid": 4, "state": "opened", "web_url": "https://gitlab.com/myorg/myrepo/-/merge_requests/4",
			"source_branch": "fix-bug", "target_branch": "main", "author": map[string]any{"username": "alice"},
			"source_project_id": 99, "target_project_id": 10,
		}})
	})
	_, p := testServer(t, mux)
	repo := ports.SCMRepo{Provider: "gitlab", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

	prs, err := p.ListOpenPRsByBranch(context.Background(), repo, "fix-bug")
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("source_branch") != "fix-bug" || query.Get("state") != "opened" {
		t.Fatalf("query = %v, want source_branch=fix-bug state=opened", query)
	}
	if len(prs) != 1 || prs[0].Number != 3 || prs[0].SourceBranch != "fix-bug" || prs[0].Author != "alice" || prs[0].HeadRepo != "myorg/myrepo" || prs[0].BaseRepo != "myorg/myrepo" {
		t.Fatalf("prs = %+v", prs)
	}
}
