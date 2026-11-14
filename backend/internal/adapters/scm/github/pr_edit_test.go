package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func strPtr(s string) *string { return &s }

func TestPullRequestContent_ReadsTitleBodyAndPermission(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		if !strings.Contains(body.Query, "title body updatedAt viewerCanUpdate") {
			t.Fatalf("query = %s", body.Query)
		}
		if body.Variables["owner"] != "octocat" || body.Variables["name"] != "hello" || body.Variables["number"] != float64(42) {
			t.Fatalf("variables = %#v", body.Variables)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"title": "Fix it", "body": "line one\r\nline two", "updatedAt": "2026-11-12T10:00:00Z", "viewerCanUpdate": true,
		}}}})
	})
	got, err := newProviderForTest(t, f).PullRequestContent(ctx(), validMergeRequest().PR)
	if err != nil {
		t.Fatal(err)
	}
	want := ports.SCMPRContent{Title: "Fix it", Body: "line one\r\nline two", UpdatedAt: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC), CanEdit: true}
	if got != want {
		t.Fatalf("content = %#v, want %#v", got, want)
	}
}

func TestPullRequestContent_MissingPullRequestIsNotFound(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": nil}}})
	})
	if _, err := newProviderForTest(t, f).PullRequestContent(ctx(), validMergeRequest().PR); !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestEditPullRequest_PatchesOnlyTheGivenFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request ports.SCMPREditRequest
		want    map[string]any
	}{
		{name: "title", request: ports.SCMPREditRequest{Title: strPtr("New title")}, want: map[string]any{"title": "New title"}},
		{name: "body", request: ports.SCMPREditRequest{Body: strPtr("New body")}, want: map[string]any{"body": "New body"}},
		{name: "cleared body", request: ports.SCMPREditRequest{Body: strPtr("")}, want: map[string]any{"body": ""}},
		{name: "both", request: ports.SCMPREditRequest{Title: strPtr("T"), Body: strPtr("B")}, want: map[string]any{"title": "T", "body": "B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body) != len(tc.want) {
					t.Fatalf("body = %#v, want %#v", body, tc.want)
				}
				for key, value := range tc.want {
					if body[key] != value {
						t.Fatalf("body = %#v, want %#v", body, tc.want)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"title": "stored title", "body": "stored body", "updated_at": "2026-11-12T10:05:00Z"})
			})
			request := tc.request
			request.PR = validMergeRequest().PR
			got, err := newProviderForTest(t, f).EditPullRequest(ctx(), request)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != "stored title" || got.Body != "stored body" || !got.UpdatedAt.Equal(time.Date(2026, 11, 12, 10, 5, 0, 0, time.UTC)) {
				t.Fatalf("content = %#v", got)
			}
		})
	}
}

func TestEditPullRequest_NullBodyInResponseIsEmpty(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "T", "body": nil})
	})
	request := ports.SCMPREditRequest{PR: validMergeRequest().PR, Body: strPtr("")}
	got, err := newProviderForTest(t, f).EditPullRequest(ctx(), request)
	if err != nil || got.Body != "" || got.Title != "T" {
		t.Fatalf("content = %#v, err %v", got, err)
	}
}

func TestEditPullRequest_RejectsEmptyEditsWithoutCallingGitHub(t *testing.T) {
	f := newFakeGH(t)
	p := newProviderForTest(t, f)
	for name, request := range map[string]ports.SCMPREditRequest{
		"nothing":     {PR: validMergeRequest().PR},
		"blank title": {PR: validMergeRequest().PR, Title: strPtr("  ")},
		"no pr":       {Title: strPtr("T")},
	} {
		if _, err := p.EditPullRequest(ctx(), request); err == nil {
			t.Fatalf("%s: edit succeeded", name)
		}
	}
	if len(f.calls()) != 0 {
		t.Fatalf("calls = %d, want none", len(f.calls()))
	}
}

func TestEditPullRequest_MapsProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, want: ports.ErrSCMNotFound},
		{name: "forbidden", status: http.StatusForbidden, want: ports.ErrSCMForbidden},
		{name: "refused", status: http.StatusUnprocessableEntity, want: ports.ErrSCMRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPatch, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "no"})
			})
			_, err := newProviderForTest(t, f).EditPullRequest(ctx(), ports.SCMPREditRequest{PR: validMergeRequest().PR, Title: strPtr("T")})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
