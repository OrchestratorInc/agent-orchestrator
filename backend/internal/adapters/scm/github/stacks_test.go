package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var stackRepo = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}

const stackHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fastStackPolls(t *testing.T) {
	t.Helper()
	wait, first := stackMergeWait, stackMergeFirstPoll
	stackMergeWait, stackMergeFirstPoll = time.Second, time.Millisecond
	t.Cleanup(func() { stackMergeWait, stackMergeFirstPoll = wait, first })
}

func stackBody(number int, numbers ...int) map[string]any {
	prs := make([]map[string]any, 0, len(numbers))
	for _, n := range numbers {
		prs = append(prs, map[string]any{"number": n, "state": "open", "draft": n == 3, "merged_at": nil, "head": map[string]any{"ref": "layer", "sha": stackHead}})
	}
	return map[string]any{"number": number, "base": map[string]any{"ref": "main"}, "open": true, "pull_requests": prs}
}

func TestPullRequestStack_ReadsLayersBottomFirst(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/stacks", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pull_request"); got != "2" {
			t.Fatalf("pull_request = %q", got)
		}
		first := stackBody(9, 1, 2, 3)
		first["pull_requests"].([]map[string]any)[0]["merged_at"] = "2026-11-04T00:00:00Z"
		first["pull_requests"].([]map[string]any)[0]["state"] = "closed"
		_ = json.NewEncoder(w).Encode([]any{first})
	})
	got, ok, err := newProviderForTest(t, f).PullRequestStack(ctx(), ports.SCMPRRef{Repo: stackRepo, Number: 2})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.URL != "https://github.com/octocat/hello/stack/9" || got.BaseBranch != "main" || len(got.Layers) != 3 {
		t.Fatalf("stack = %#v", got)
	}
	if !got.Layers[0].Merged || got.Layers[0].Open || !got.Layers[1].Open || !got.Layers[2].Draft {
		t.Fatalf("layers = %#v", got.Layers)
	}
}

func TestPullRequestStack_NoStackOrNoFeatureIsNotAnError(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"none": func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]any{}) },
		"unavailable": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodGet, "/repos/octocat/hello/stacks", handler)
			if _, ok, err := newProviderForTest(t, f).PullRequestStack(ctx(), ports.SCMPRRef{Repo: stackRepo, Number: 2}); ok || err != nil {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestFindOpenPullRequest_NeedsExactlyOne(t *testing.T) {
	for name, tc := range map[string]struct {
		pulls []map[string]any
		want  bool
	}{
		"one":  {pulls: []map[string]any{{"number": 5, "html_url": "https://github.com/octocat/hello/pull/5"}}, want: true},
		"none": {pulls: []map[string]any{}},
		"two":  {pulls: []map[string]any{{"number": 5}, {"number": 6}}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("head"); got != "octocat:feature" {
					t.Fatalf("head = %q", got)
				}
				_ = json.NewEncoder(w).Encode(tc.pulls)
			})
			got, ok, err := newProviderForTest(t, f).FindOpenPullRequest(ctx(), stackRepo, "feature")
			if err != nil || ok != tc.want || (ok && got.Number != 5) {
				t.Fatalf("got=%#v ok=%v err=%v", got, ok, err)
			}
		})
	}
}

func TestRegisterStacked_StartsOrExtendsTheParentsStack(t *testing.T) {
	decode := func(t *testing.T, r *http.Request) []int {
		t.Helper()
		var body struct {
			PullRequests []int `json:"pull_requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.PullRequests
	}
	t.Run("starts one", func(t *testing.T) {
		f := newFakeGH(t)
		f.on(http.MethodGet, "/repos/octocat/hello/stacks", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]any{}) })
		f.on(http.MethodPost, "/repos/octocat/hello/stacks", func(w http.ResponseWriter, r *http.Request) {
			if got := decode(t, r); len(got) != 2 || got[0] != 1 || got[1] != 2 {
				t.Fatalf("pull_requests = %v", got)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(stackBody(9, 1, 2))
		})
		got, err := newProviderForTest(t, f).RegisterStacked(ctx(), ports.SCMStackRegisterRequest{Repo: stackRepo, Parent: 1, Child: 2})
		if err != nil || got.Number != 9 {
			t.Fatalf("got=%#v err=%v", got, err)
		}
	})
	t.Run("extends one", func(t *testing.T) {
		f := newFakeGH(t)
		f.on(http.MethodGet, "/repos/octocat/hello/stacks", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]any{stackBody(9, 1, 2)}) })
		f.on(http.MethodPost, "/repos/octocat/hello/stacks/9/add", func(w http.ResponseWriter, r *http.Request) {
			if got := decode(t, r); len(got) != 1 || got[0] != 3 {
				t.Fatalf("pull_requests = %v", got)
			}
			_ = json.NewEncoder(w).Encode(stackBody(9, 1, 2, 3))
		})
		if _, err := newProviderForTest(t, f).RegisterStacked(ctx(), ports.SCMStackRegisterRequest{Repo: stackRepo, Parent: 2, Child: 3}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("github refuses", func(t *testing.T) {
		f := newFakeGH(t)
		f.on(http.MethodGet, "/repos/octocat/hello/stacks", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]any{}) })
		f.on(http.MethodPost, "/repos/octocat/hello/stacks", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"cannot form a stack"}`, http.StatusUnprocessableEntity)
		})
		_, err := newProviderForTest(t, f).RegisterStacked(ctx(), ports.SCMStackRegisterRequest{Repo: stackRepo, Parent: 1, Child: 2})
		if !errors.Is(err, ports.ErrSCMRejected) {
			t.Fatalf("err = %v", err)
		}
	})
}

func stackMergeRequest() ports.SCMStackMergeRequest {
	return ports.SCMStackMergeRequest{PR: ports.SCMPRRef{Repo: stackRepo, Number: 2}, ExpectedHeadSHA: stackHead, Method: ports.SCMMergeSquash}
}

func TestMergeStack_FollowsThePendingMergeToItsEnd(t *testing.T) {
	fastStackPolls(t)
	f := newFakeGH(t)
	f.on(http.MethodPut, "/repos/octocat/hello/pulls/2/merge-async", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["sha"] != stackHead || body["merge_method"] != "squash" || body["merge_action"] != "default" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "pending", "details": map[string]any{"uuid": "u-1"}})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/2/merge-async/u-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "merged", "details": map[string]any{}})
	})
	got, err := newProviderForTest(t, f).MergeStack(ctx(), stackMergeRequest())
	if err != nil || got != ports.SCMStackMerged {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestMergeStack_ReportsTheOutcome(t *testing.T) {
	fastStackPolls(t)
	for name, tc := range map[string]struct {
		status  int
		body    map[string]any
		want    ports.SCMStackMergeStatus
		wantErr error
	}{
		"queued":       {status: http.StatusOK, body: map[string]any{"status": "enqueued", "details": map[string]any{}}, want: ports.SCMStackEnqueued},
		"failed":       {status: http.StatusOK, body: map[string]any{"status": "failed", "details": map[string]any{"message": "checks"}}, wantErr: ports.ErrSCMNotMergeable},
		"head changed": {status: http.StatusConflict, body: map[string]any{"message": "stale"}, wantErr: ports.ErrSCMHeadChanged},
		"blocked":      {status: http.StatusUnprocessableEntity, body: map[string]any{"message": "no"}, wantErr: ports.ErrSCMNotMergeable},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPut, "/repos/octocat/hello/pulls/2/merge-async", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			})
			got, err := newProviderForTest(t, f).MergeStack(ctx(), stackMergeRequest())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}

func TestMergeStack_StopsWaitingButDoesNotCallItFailed(t *testing.T) {
	fastStackPolls(t)
	stackMergeWait = 5 * time.Millisecond
	f := newFakeGH(t)
	pending := map[string]any{"status": "pending", "details": map[string]any{"uuid": "u-1"}}
	f.on(http.MethodPut, "/repos/octocat/hello/pulls/2/merge-async", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(pending) })
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/2/merge-async/u-1", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(pending) })
	got, err := newProviderForTest(t, f).MergeStack(ctx(), stackMergeRequest())
	if err != nil || got != ports.SCMStackPending {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
