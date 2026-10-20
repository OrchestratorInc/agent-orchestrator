package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestListPRsByRepoDoesNotFillETagBodyCache pins the #97 memory-leak fix:
// the open-PR list is paginated with a cursor (page, since) that changes on
// every call, so routing it through the shared ETag cache retained one full
// response body per (page, since) tuple in bodyOut — hundreds of MiB on a
// long-lived daemon. Cursor-keyed list responses must not be cached, while
// every page and observation still comes back and requests keep their query
// params and auth.
func TestListPRsByRepoDoesNotFillETagBodyCache(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octo/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tkn-test" {
			t.Errorf("Authorization = %q, want Bearer tkn-test", r.Header.Get("Authorization"))
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100", got)
		}
		if got := r.URL.Query().Get("since"); got == "" {
			t.Error("since cursor missing from request query")
		}
		page := r.URL.Query().Get("page")
		count := 3
		if page == "1" {
			count = 100
		}
		pulls := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			n := i + 1
			if page == "2" {
				n += 100
			}
			pulls = append(pulls, map[string]any{
				"number":   n,
				"state":    "open",
				"title":    fmt.Sprintf("PR %d", n),
				"html_url": fmt.Sprintf("https://github.com/octo/repo/pull/%d", n),
				"node_id":  "NODE" + strconv.Itoa(n),
				"head":     map[string]any{"ref": "feature", "sha": "abc", "repo": map[string]any{"full_name": "octo/repo"}},
				"base":     map[string]any{"ref": "main", "sha": "def", "repo": map[string]any{"full_name": "octo/repo"}},
				"user":     map[string]any{"login": "octocat"},
			})
		}
		w.Header().Set("ETag", `"etag-page-`+page+`"`)
		_ = json.NewEncoder(w).Encode(pulls)
	})
	p := newProviderForTest(t, f)
	c := p.client
	repo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octo", Name: "repo", Repo: "octo/repo"}

	// Multi-page listing under a since cursor: page 1 fills up (100), page 2
	// ends it. Both pages must be returned in connection order.
	since := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	obs, err := p.ListPRsByRepo(ctx(), repo, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 103 {
		t.Fatalf("observations = %d, want 103 (both pages)", len(obs))
	}
	if obs[0].Number != 1 || obs[102].Number != 103 {
		t.Errorf("pages out of order: first=%d last=%d", obs[0].Number, obs[102].Number)
	}
	if len(c.bodyOut) != 0 {
		t.Errorf("bodyOut grew to %d entries after paginated listing; cursor-keyed list responses must not enter the shared ETag cache", len(c.bodyOut))
	}

	// Repeated listings with different cursors must not retain anything either.
	for i := 1; i <= 3; i++ {
		if _, err := p.ListPRsByRepo(ctx(), repo, since.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if len(c.bodyOut) != 0 {
			t.Fatalf("bodyOut = %d entries after listing %d; want 0", len(c.bodyOut), i)
		}
	}
	// 2 pages per listing × (initial + 3 cursor repeats) = 8 requests total.
	if n := f.callsTo(http.MethodGet, "/repos/octo/repo/pulls"); n != 8 {
		t.Errorf("pulls endpoint hit %d times, want 8", n)
	}
}
