package gitlab

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func strPtr(s string) *string { return &s }

func TestPullRequestContent_ReadsTitleAndDescription(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != gitlabMRPath {
			t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "title": "Fix it", "description": "Why", "updated_at": "2026-11-12T10:00:00Z"})
	}))
	got, err := p.PullRequestContent(ctx(), validMergeRequest().PR)
	if err != nil {
		t.Fatal(err)
	}
	want := ports.SCMPRContent{Title: "Fix it", Body: "Why", UpdatedAt: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC), CanEdit: true}
	if got != want {
		t.Fatalf("content = %#v, want %#v", got, want)
	}
}

func TestEditPullRequest_PutsOnlyTheGivenFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request ports.SCMPREditRequest
		want    map[string]any
	}{
		{name: "title", request: ports.SCMPREditRequest{Title: strPtr("New title")}, want: map[string]any{"title": "New title"}},
		{name: "description", request: ports.SCMPREditRequest{Body: strPtr("New body")}, want: map[string]any{"description": "New body"}},
		{name: "cleared description", request: ports.SCMPREditRequest{Body: strPtr("")}, want: map[string]any{"description": ""}},
		{name: "both", request: ports.SCMPREditRequest{Title: strPtr("T"), Body: strPtr("B")}, want: map[string]any{"title": "T", "description": "B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.EscapedPath() != gitlabMRPath {
					t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
				}
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
				_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "title": "stored title", "description": "stored body", "updated_at": "2026-11-12T10:05:00Z"})
			}))
			request := tc.request
			request.PR = validMergeRequest().PR
			got, err := p.EditPullRequest(ctx(), request)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != "stored title" || got.Body != "stored body" || !got.UpdatedAt.Equal(time.Date(2026, 11, 12, 10, 5, 0, 0, time.UTC)) {
				t.Fatalf("content = %#v", got)
			}
		})
	}
}

func TestEditPullRequest_GitLabFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, want: ports.ErrSCMNotFound},
		{name: "forbidden", status: http.StatusForbidden, want: ports.ErrSCMForbidden},
		{name: "refused", status: http.StatusBadRequest, want: ports.ErrSCMRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "no"})
			}))
			_, err := p.EditPullRequest(ctx(), ports.SCMPREditRequest{PR: validMergeRequest().PR, Title: strPtr("T")})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
	_, p := testServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("an empty edit must not reach GitLab")
	}))
	if _, err := p.EditPullRequest(ctx(), ports.SCMPREditRequest{PR: validMergeRequest().PR}); err == nil {
		t.Fatal("edit with no field succeeded")
	}
	if _, err := p.EditPullRequest(ctx(), ports.SCMPREditRequest{PR: validMergeRequest().PR, Title: strPtr(" ")}); err == nil {
		t.Fatal("edit with a blank title succeeded")
	}
}
