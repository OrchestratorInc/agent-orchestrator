package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func repoProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := NewProvider(ProviderOptions{RESTBase: srv.URL, SkipTokenPreflight: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testRepo() ports.SCMRepo {
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}
}

func repoComment(id int64, login, kind, body, link string, at time.Time) map[string]any {
	return map[string]any{
		"id":         id,
		"body":       body,
		"html_url":   link,
		"issue_url":  "https://api.github.com/repos/o/r/issues/0",
		"created_at": at.Format(time.RFC3339),
		"user":       map[string]any{"login": login, "type": kind},
	}
}

func TestListRepoMentionsKeepsHumanPRMentionsOnly(t *testing.T) {
	since := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	var query string
	p := repoProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues/comments" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]map[string]any{
			repoComment(1, "alice", "User", "@ao раскатай на дев", "https://github.com/o/r/pull/1035#issuecomment-1", since.Add(time.Minute)),
			repoComment(2, "bob", "User", "просто обсуждаем", "https://github.com/o/r/pull/1035#issuecomment-2", since.Add(2*time.Minute)),
			repoComment(3, "ci", "Bot", "@ao build done", "https://github.com/o/r/pull/1035#issuecomment-3", since.Add(3*time.Minute)),
			repoComment(4, "carol", "User", "@ao посмотри", "https://github.com/o/r/issues/12#issuecomment-4", since.Add(4*time.Minute)),
			// Edited today, written yesterday: the edit must not re-issue it.
			repoComment(5, "dave", "User", "@ao old", "https://github.com/o/r/pull/9#issuecomment-5", since.Add(-time.Hour)),
		})
	})

	got, err := p.ListRepoMentions(context.Background(), testRepo(), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("mentions = %+v, want only alice's PR comment", got)
	}
	if got[0].PRNumber != 1035 || got[0].PRURL != "https://github.com/o/r/pull/1035" || got[0].Mention.Author != "alice" || got[0].Mention.ID != "1" {
		t.Fatalf("mention = %+v", got[0])
	}
	if !strings.Contains(query, "since=2026-10-02T08%3A00%3A00Z") {
		t.Fatalf("query = %q, want the since bound", query)
	}
}

func TestPullFromComment(t *testing.T) {
	cases := []struct {
		link   string
		number int
		ok     bool
	}{
		{"https://github.com/o/r/pull/7#issuecomment-1", 7, true},
		{"https://github.com/o/r/pull/7", 7, true},
		{"https://github.com/o/r/issues/7#issuecomment-1", 0, false},
		{"https://github.com/o/r/pull/x#issuecomment-1", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		n, url, ok := pullFromComment(restIssueComment{HTMLURL: tc.link})
		if n != tc.number || ok != tc.ok {
			t.Fatalf("%q: got (%d, %q, %v)", tc.link, n, url, ok)
		}
		if ok && url != "https://github.com/o/r/pull/7" {
			t.Fatalf("%q: url = %q", tc.link, url)
		}
	}
}

func TestCanPushMapsGitHubRoles(t *testing.T) {
	roles := map[string]map[string]string{
		"writer":     {"permission": "write", "role_name": "write"},
		"maintainer": {"permission": "write", "role_name": "maintain"},
		"owner":      {"permission": "admin", "role_name": "admin"},
		"triager":    {"permission": "read", "role_name": "triage"},
		"reader":     {"permission": "read", "role_name": "read"},
	}
	p := repoProvider(t, func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		// /repos/o/r/collaborators/<login>/permission
		if len(parts) != 7 || parts[4] != "collaborators" || parts[6] != "permission" {
			http.NotFound(w, r)
			return
		}
		body, ok := roles[parts[5]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	want := map[string]bool{"writer": true, "maintainer": true, "owner": true, "triager": false, "reader": false, "stranger": false}
	for login, expect := range want {
		got, err := p.CanPush(context.Background(), testRepo(), login)
		if err != nil {
			t.Fatalf("%s: %v", login, err)
		}
		if got != expect {
			t.Fatalf("%s: CanPush = %v, want %v", login, got, expect)
		}
	}
}

func TestFetchPullHeadCarriesHeadRepo(t *testing.T) {
	p := repoProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls/1035" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/o/r/pull/1035",
			"number":   1035,
			"state":    "open",
			"draft":    false,
			"title":    "Fix login",
			"head":     map[string]any{"ref": "feature/login", "sha": "abc", "repo": map[string]any{"full_name": "o/r"}},
			"base":     map[string]any{"ref": "main"},
			"user":     map[string]any{"login": "alice"},
		})
	})
	got, err := p.FetchPullHead(context.Background(), ports.SCMPRRef{Repo: testRepo(), Number: 1035})
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceBranch != "feature/login" || got.HeadRepo != "o/r" || got.Closed || got.Draft || got.Title != "Fix login" {
		t.Fatalf("pull = %+v", got)
	}
}
