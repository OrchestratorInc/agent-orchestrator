package gitlab

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const gitlabMRPath = "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"

func TestMergePullRequest_MergeMethodDisablesSquash(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("squash") != "false" || q.Get("sha") != validMergeRequest().ExpectedHeadSHA {
			t.Fatalf("query = %v", q)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"merge_commit_sha": "abc"})
	}))
	request := validMergeRequest()
	request.Method = ports.SCMMergeMerge
	if _, err := p.MergePullRequest(ctx(), request); err != nil {
		t.Fatal(err)
	}
}

func TestMergePullRequest_RebaseIsUnsupported(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rebase must not reach GitLab")
	}))
	request := validMergeRequest()
	request.Method = ports.SCMMergeRebase
	if _, err := p.MergePullRequest(ctx(), request); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
}

func TestChangePullRequestState_CloseAndReopenUseStateEvent(t *testing.T) {
	for _, tc := range []struct {
		action ports.SCMPRStateAction
		event  string
		state  string
	}{{ports.SCMPRClose, "close", "closed"}, {ports.SCMPRReopen, "reopen", "opened"}} {
		t.Run(string(tc.action), func(t *testing.T) {
			_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.EscapedPath() != gitlabMRPath {
					t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body) != 1 || body["state_event"] != tc.event {
					t.Fatalf("body = %#v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "state": tc.state})
			}))
			err := p.ChangePullRequestState(ctx(), ports.SCMPRStateRequest{PR: validMergeRequest().PR, Action: tc.action})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChangePullRequestState_GitLabFailuresAndUnsupported(t *testing.T) {
	for _, action := range []ports.SCMPRStateAction{ports.SCMPRReady, ports.SCMPRDraft} {
		_, p := testServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("draft toggles must not reach GitLab")
		}))
		err := p.ChangePullRequestState(ctx(), ports.SCMPRStateRequest{PR: validMergeRequest().PR, Action: action})
		if !errors.Is(err, ports.ErrSCMUnsupported) {
			t.Fatalf("%s error = %v, want unsupported", action, err)
		}
	}
	for _, tc := range []struct {
		status int
		body   map[string]any
		want   error
	}{
		{http.StatusNotFound, map[string]any{"message": "404 Not found"}, ports.ErrSCMNotFound},
		{http.StatusMethodNotAllowed, map[string]any{"message": "405 Method Not Allowed"}, ports.ErrSCMRejected},
		{http.StatusOK, map[string]any{"state": "merged"}, ports.ErrSCMRejected},
	} {
		_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(tc.body)
		}))
		err := p.ChangePullRequestState(ctx(), ports.SCMPRStateRequest{PR: validMergeRequest().PR, Action: ports.SCMPRClose})
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d error = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestPostComment_GitLabNotesAndDiscussionReplies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		threadID string
		path     string
	}{
		{name: "top level", path: gitlabMRPath + "/notes"},
		{name: "reply", threadID: "abc123", path: gitlabMRPath + "/discussions/abc123/notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.EscapedPath() != tc.path {
					t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.EscapedPath(), tc.path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["body"] != "hello" {
					t.Fatalf("body = %#v", body)
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": 7, "body": "hello", "created_at": "2026-10-30T12:00:00Z", "author": map[string]any{"username": "dev"},
				})
			}))
			got, err := p.PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "hello", ThreadID: tc.threadID})
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != "7" || got.Author != "dev" || got.URL != validMergeRequest().PR.URL+"#note_7" || !got.CreatedAt.Equal(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("result = %#v", got)
			}
		})
	}
}

func TestPostComment_GitLabNotFound(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "404 Not found"})
	}))
	_, err := p.PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "hello"})
	if !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
}
