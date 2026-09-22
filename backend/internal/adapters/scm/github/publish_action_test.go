package github

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func publishRequest() ports.SCMPublishRequest {
	return ports.SCMPublishRequest{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, SourceBranch: "ao/card", TargetBranch: "main", HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Title: "Add delivery card", Body: "Summary\n\nTests"}
}

func TestReconcileOrCreatePullRequestReturnsExistingMatchingPR(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"number": 42, "state": "open", "html_url": "https://github.com/octocat/hello/pull/42", "title": "Existing", "head": map[string]any{"ref": "ao/card", "sha": publishRequest().HeadSHA, "repo": map[string]any{"full_name": "octocat/hello"}}, "base": map[string]any{"ref": "main"}}})
	})
	got, err := newProviderForTest(t, f).ReconcileOrCreatePullRequest(ctx(), publishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Reconciled || got.PR.Number != 42 || got.PR.SourceBranch != "ao/card" {
		t.Fatalf("result = %#v", got)
	}
}

func TestReconcileOrCreatePullRequestCreatesAfterNoMatch(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	f.on(http.MethodPost, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Head  string `json:"head"`
			Base  string `json:"base"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Head != "ao/card" || body.Base != "main" || body.Title != "Add delivery card" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 43, "state": "open", "html_url": "https://github.com/octocat/hello/pull/43", "title": body.Title, "head": map[string]any{"ref": body.Head, "sha": publishRequest().HeadSHA, "repo": map[string]any{"full_name": "octocat/hello"}}, "base": map[string]any{"ref": body.Base}})
	})
	got, err := newProviderForTest(t, f).ReconcileOrCreatePullRequest(ctx(), publishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got.Reconciled || got.PR.Number != 43 || got.PR.HeadSHA != publishRequest().HeadSHA {
		t.Fatalf("result = %#v", got)
	}
}
