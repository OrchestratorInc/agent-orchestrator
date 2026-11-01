package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type graphQLBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func decodeGraphQLBody(t *testing.T, r *http.Request) graphQLBody {
	t.Helper()
	var body graphQLBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestMergePullRequest_SendsRequestedMethodWithHeadGuard(t *testing.T) {
	for _, method := range []ports.SCMMergeMethod{ports.SCMMergeSquash, ports.SCMMergeMerge, ports.SCMMergeRebase} {
		t.Run(string(method), func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPut, "/repos/octocat/hello/pulls/42/merge", func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					SHA         string `json:"sha"`
					MergeMethod string `json:"merge_method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.SHA != validMergeRequest().ExpectedHeadSHA || body.MergeMethod != string(method) {
					t.Fatalf("body = %#v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"sha": "merge-sha", "merged": true})
			})
			request := validMergeRequest()
			request.Method = method
			if _, err := newProviderForTest(t, f).MergePullRequest(ctx(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMergePullRequest_RejectsUnknownMethodWithoutCallingGitHub(t *testing.T) {
	f := newFakeGH(t)
	request := validMergeRequest()
	request.Method = "octopus"
	if _, err := newProviderForTest(t, f).MergePullRequest(ctx(), request); err == nil {
		t.Fatal("merge with unknown method succeeded")
	}
	if len(f.calls()) != 0 {
		t.Fatalf("calls = %d, want none", len(f.calls()))
	}
}

func stateRequest(action ports.SCMPRStateAction) ports.SCMPRStateRequest {
	return ports.SCMPRStateRequest{PR: validMergeRequest().PR, Action: action}
}

func TestChangePullRequestState_CloseAndReopenPatchRESTState(t *testing.T) {
	for _, tc := range []struct {
		action ports.SCMPRStateAction
		state  string
	}{{ports.SCMPRClose, "closed"}, {ports.SCMPRReopen, "open"}} {
		t.Run(string(tc.action), func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body) != 1 || body["state"] != tc.state {
					t.Fatalf("body = %#v, want only state=%s", body, tc.state)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "state": tc.state})
			})
			if err := newProviderForTest(t, f).ChangePullRequestState(ctx(), stateRequest(tc.action)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChangePullRequestState_MapsRESTFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   map[string]any
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, body: map[string]any{"message": "Not Found"}, want: ports.ErrSCMNotFound},
		{name: "refused", status: http.StatusUnprocessableEntity, body: map[string]any{"message": "state cannot be changed"}, want: ports.ErrSCMRejected},
		{name: "unconfirmed", status: http.StatusOK, body: map[string]any{"state": "open"}, want: ports.ErrSCMRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			})
			err := newProviderForTest(t, f).ChangePullRequestState(ctx(), stateRequest(ports.SCMPRClose))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestChangePullRequestState_ReadyUsesKnownNodeID(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		if !strings.Contains(body.Query, "markPullRequestReadyForReview(input: { pullRequestId: $id })") || body.Variables["id"] != "PR_node" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"markPullRequestReadyForReview": map[string]any{
			"pullRequest": map[string]any{"id": "PR_node", "isDraft": false},
		}}})
	})
	request := stateRequest(ports.SCMPRReady)
	request.NodeID = "PR_node"
	if err := newProviderForTest(t, f).ChangePullRequestState(ctx(), request); err != nil {
		t.Fatal(err)
	}
	if n := f.callsTo(http.MethodPost, "/graphql"); n != 1 {
		t.Fatalf("graphql calls = %d, want only the mutation", n)
	}
}

func TestChangePullRequestState_DraftLooksUpNodeIDWhenUnknown(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		switch {
		case strings.Contains(body.Query, "PullRequestNodeID"):
			if body.Variables["owner"] != "octocat" || body.Variables["name"] != "hello" || body.Variables["number"] != float64(42) {
				t.Fatalf("lookup variables = %#v", body.Variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"id": "PR_looked_up"}}}})
		case strings.Contains(body.Query, "convertPullRequestToDraft"):
			if body.Variables["id"] != "PR_looked_up" {
				t.Fatalf("mutation variables = %#v", body.Variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"convertPullRequestToDraft": map[string]any{
				"pullRequest": map[string]any{"id": "PR_looked_up", "isDraft": true},
			}}})
		default:
			t.Fatalf("unexpected query %s", body.Query)
		}
	})
	if err := newProviderForTest(t, f).ChangePullRequestState(ctx(), stateRequest(ports.SCMPRDraft)); err != nil {
		t.Fatal(err)
	}
	if n := f.callsTo(http.MethodPost, "/graphql"); n != 2 {
		t.Fatalf("graphql calls = %d, want lookup + mutation", n)
	}
}

func TestChangePullRequestState_DraftMutationFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response map[string]any
		want     error
	}{
		{name: "graphql refusal", response: map[string]any{"errors": []any{map[string]any{"message": "Pull request is not in draft state"}}}, want: ports.ErrSCMRejected},
		{name: "not confirmed", response: map[string]any{"data": map[string]any{"markPullRequestReadyForReview": map[string]any{"pullRequest": map[string]any{"id": "PR_node", "isDraft": true}}}}, want: ports.ErrSCMRejected},
		{name: "not found", response: map[string]any{"errors": []any{map[string]any{"message": "Could not resolve to a node with the global id"}}}, want: ports.ErrSCMNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.response)
			})
			request := stateRequest(ports.SCMPRReady)
			request.NodeID = "PR_node"
			err := newProviderForTest(t, f).ChangePullRequestState(ctx(), request)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPostComment_TopLevelUsesIssueCommentsEndpoint(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/repos/octocat/hello/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["body"] != "Looks good" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 99, "node_id": "IC_99", "html_url": "https://github.com/octocat/hello/pull/42#issuecomment-99",
			"body": "Looks good", "created_at": "2026-10-30T12:00:00Z",
			"user": map[string]any{"login": "octocat", "type": "User"},
		})
	})
	got, err := newProviderForTest(t, f).PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "Looks good"})
	if err != nil {
		t.Fatal(err)
	}
	want := ports.SCMCommentResult{
		ID: "IC_99", URL: "https://github.com/octocat/hello/pull/42#issuecomment-99", Author: "octocat",
		Body: "Looks good", CreatedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Fatalf("result = %#v, want %#v", got, want)
	}
}

func TestPostComment_ThreadReplyUsesGraphQLMutation(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		if !strings.Contains(body.Query, "addPullRequestReviewThreadReply(input: { pullRequestReviewThreadId: $threadId, body: $body })") ||
			body.Variables["threadId"] != "PRRT_1" || body.Variables["body"] != "Fixed" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"addPullRequestReviewThreadReply": map[string]any{
			"comment": map[string]any{
				"id": "PRRC_2", "url": "https://github.com/octocat/hello/pull/42#discussion_r2", "body": "Fixed",
				"createdAt": "2026-10-30T12:05:00Z", "author": map[string]any{"login": "ao-bot", "__typename": "Bot"},
			},
		}}})
	})
	got, err := newProviderForTest(t, f).PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "Fixed", ThreadID: "PRRT_1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "PRRC_2" || got.Author != "ao-bot" || !got.IsBot || got.URL == "" || !got.CreatedAt.Equal(time.Date(2026, 10, 30, 12, 5, 0, 0, time.UTC)) {
		t.Fatalf("result = %#v", got)
	}
}

func TestPostComment_MapsFailures(t *testing.T) {
	t.Run("rest not found", func(t *testing.T) {
		f := newFakeGH(t)
		f.on(http.MethodPost, "/repos/octocat/hello/issues/42/comments", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		})
		_, err := newProviderForTest(t, f).PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "hi"})
		if !errors.Is(err, ports.ErrSCMNotFound) {
			t.Fatalf("error = %v, want not found", err)
		}
	})
	t.Run("rest refused", func(t *testing.T) {
		f := newFakeGH(t)
		f.on(http.MethodPost, "/repos/octocat/hello/issues/42/comments", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed"}`))
		})
		_, err := newProviderForTest(t, f).PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "hi"})
		if !errors.Is(err, ports.ErrSCMRejected) {
			t.Fatalf("error = %v, want rejected", err)
		}
	})
	t.Run("graphql refused", func(t *testing.T) {
		f := newFakeGH(t)
		f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Thread is locked"}]}`))
		})
		_, err := newProviderForTest(t, f).PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "hi", ThreadID: "PRRT_1"})
		if !errors.Is(err, ports.ErrSCMRejected) {
			t.Fatalf("error = %v, want rejected", err)
		}
	})
	t.Run("blank body", func(t *testing.T) {
		f := newFakeGH(t)
		if _, err := newProviderForTest(t, f).PostComment(ctx(), ports.SCMCommentRequest{PR: validMergeRequest().PR, Body: "  \n"}); err == nil {
			t.Fatal("blank comment succeeded")
		}
		if len(f.calls()) != 0 {
			t.Fatalf("calls = %d, want none", len(f.calls()))
		}
	})
}

func TestReviewThreadsQueryRequestsCommentCreatedAt(t *testing.T) {
	query := buildReviewThreadsQuery(validMergeRequest().PR, "", true)
	if !strings.Contains(query, "comments(first:5){ nodes{ id body url createdAt ") {
		t.Fatalf("review threads query does not request comment createdAt:\n%s", query)
	}
	thread := scmThreadFromGraphQL(map[string]any{
		"id": "PRRT_1", "isResolved": false, "path": "a.go", "line": float64(3),
		"comments": map[string]any{"nodes": []any{map[string]any{
			"id": "PRRC_1", "body": "nit", "createdAt": "2026-10-29T08:00:00Z", "author": map[string]any{"login": "rev", "__typename": "User"},
		}}},
	})
	if len(thread.Comments) != 1 || !thread.Comments[0].CreatedAt.Equal(time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("thread = %#v", thread)
	}
}

func TestPullRequestDetailsCarryConversationComments(t *testing.T) {
	query, _ := buildSCMBatchQuery([]ports.SCMPRRef{validMergeRequest().PR})
	if !strings.Contains(query, "conversation: comments(last:50){ nodes{ id body url createdAt author{ login __typename } } }") {
		t.Fatalf("batch query is missing conversation comments:\n%s", query)
	}
	long := strings.Repeat("é", domain.MaxPullRequestCommentBodyBytes)
	nodes := []any{
		map[string]any{"id": "", "body": "dropped: no id"},
		map[string]any{"id": "IC_1", "body": "first", "url": "https://x/1", "createdAt": "2026-10-29T08:00:00Z", "author": map[string]any{"login": "alice", "__typename": "User"}},
		map[string]any{"id": "IC_2", "body": long, "createdAt": "2026-10-29T09:00:00Z", "author": map[string]any{"login": "ci", "__typename": "Bot"}},
	}
	details := pullRequestDetailsFromGraphQL(map[string]any{"conversation": map[string]any{"nodes": nodes}})
	if len(details.Conversation) != 2 {
		t.Fatalf("conversation = %#v", details.Conversation)
	}
	first, second := details.Conversation[0], details.Conversation[1]
	if first.ID != "IC_1" || first.Author != "alice" || first.IsBot || first.URL != "https://x/1" || !first.CreatedAt.Equal(time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("first = %#v", first)
	}
	if !second.IsBot || len(second.Body) > domain.MaxPullRequestCommentBodyBytes || !strings.HasPrefix(long, second.Body) {
		t.Fatalf("second body len=%d bot=%v, want a bounded prefix from a bot", len(second.Body), second.IsBot)
	}
	if empty := pullRequestDetailsFromGraphQL(map[string]any{}); empty.Conversation == nil || len(empty.Conversation) != 0 {
		t.Fatalf("conversation without comments = %#v, want an empty list", empty.Conversation)
	}
}
