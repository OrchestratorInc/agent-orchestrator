package gitlab

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReconcileOrCreatePullRequestCreatesGitLabMergeRequest(t *testing.T) {
	calls := 0
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["source_branch"] != "ao/card" || body["target_branch"] != "main" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "iid": 7, "state": "opened", "web_url": "https://gitlab.com/myorg/myrepo/-/merge_requests/7", "source_branch": "ao/card", "target_branch": "main", "sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "title": "Add delivery card"})
	}))
	req := ports.SCMPublishRequest{Repo: ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}, SourceBranch: "ao/card", TargetBranch: "main", HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Title: "Add delivery card", Body: "Summary"}
	got, err := p.ReconcileOrCreatePullRequest(ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.PR.Number != 7 || got.PR.SourceBranch != "ao/card" {
		t.Fatalf("result = %#v", got)
	}
}
