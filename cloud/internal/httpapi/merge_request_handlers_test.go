package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const mergeTestRequest = "22222222-3333-4444-8555-666666666666"

type mergeRequestTestStore struct {
	Store
	mergeErr error
	decided  []string
}

func (s *mergeRequestTestStore) PullRequestForMerge(context.Context, domain.Principal, string, string, int, string) (domain.PullRequest, error) {
	if s.mergeErr != nil {
		return domain.PullRequest{}, s.mergeErr
	}
	return domain.PullRequest{ID: "pr-1", Provider: "github", State: "open", HeadSHA: "current", Repository: "owner/repo"}, nil
}

func (s *mergeRequestTestStore) request() domain.MergeRequest {
	return domain.MergeRequest{
		ID: mergeTestRequest, ProjectID: "project-1", SessionID: composerTestSession, SessionName: "Login flow",
		PullRequestID: "pr-1", PRNumber: 412, PRURL: "https://github.com/owner/repo/pull/412", PRTitle: "Login flow",
		Repository: "owner/repo", RequestedByUserID: "u-guest", RequestedByName: "Vaanya", Status: "pending",
		CreatedAt: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
	}
}

func (s *mergeRequestTestStore) RequestPullRequestMerge(context.Context, domain.Principal, string, string, int, string) (domain.MergeRequest, error) {
	return s.request(), nil
}

func (s *mergeRequestTestStore) ListProjectMergeRequests(context.Context, domain.Principal, string, string) ([]domain.MergeRequest, error) {
	return []domain.MergeRequest{s.request()}, nil
}

func (s *mergeRequestTestStore) PendingMergeRequestForDecision(context.Context, domain.Principal, string, string) (domain.MergeRequest, error) {
	return s.request(), nil
}

func (s *mergeRequestTestStore) DecideMergeRequest(_ context.Context, _ domain.Principal, _, _, status string) (domain.MergeRequest, error) {
	s.decided = append(s.decided, status)
	request := s.request()
	request.Status = status
	return request, nil
}

func mergeRoute(t *testing.T, store Store, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	server := testServer(store)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, domain.Principal{UserID: composerTestUser})))
		})
	})
	router.Post("/orgs/{orgId}/sessions/{sessionId}/pull-requests/{number}/merge", server.mergeSessionPullRequest)
	router.Post("/orgs/{orgId}/sessions/{sessionId}/pull-requests/{number}/merge-requests", server.requestPullRequestMerge)
	router.Get("/orgs/{orgId}/projects/{projectId}/merge-requests", server.listProjectMergeRequests)
	router.Post("/orgs/{orgId}/merge-requests/{requestId}/approve", server.approveMergeRequest)
	router.Post("/orgs/{orgId}/merge-requests/{requestId}/reject", server.rejectMergeRequest)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// A guest's merge is refused with a code the app answers with Request merge;
// the request, its listing and the owner's decision go through their routes,
// and an approval that cannot merge records nothing.
func TestMergeRequestRoutes(t *testing.T) {
	t.Parallel()
	org, session := "/orgs/"+composerTestOrg, "/sessions/"+composerTestSession

	refused := mergeRoute(t, &mergeRequestTestStore{mergeErr: postgres.ErrMergeNeedsApproval}, http.MethodPost,
		org+session+"/pull-requests/412/merge", `{"prUrl":"https://github.com/owner/repo/pull/412","expectedHeadSha":"current"}`)
	if refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), `"MERGE_NEEDS_APPROVAL"`) {
		t.Fatalf("a guest's merge: %d %s", refused.Code, refused.Body.String())
	}

	store := &mergeRequestTestStore{}
	asked := mergeRoute(t, store, http.MethodPost, org+session+"/pull-requests/412/merge-requests", `{"prUrl":"https://github.com/owner/repo/pull/412"}`)
	if asked.Code != http.StatusOK || !strings.Contains(asked.Body.String(), `"requestedBy":{"userId":"u-guest","name":"Vaanya"}`) ||
		!strings.Contains(asked.Body.String(), `"pullRequest":{"id":"pr-1","number":412`) {
		t.Fatalf("asking: %d %s", asked.Code, asked.Body.String())
	}
	if code := mergeRoute(t, store, http.MethodPost, org+session+"/pull-requests/412/merge-requests", `{}`).Code; code != http.StatusBadRequest {
		t.Fatalf("asking without a PR URL: %d", code)
	}

	listed := mergeRoute(t, store, http.MethodGet, org+"/projects/33333333-4444-4555-8666-777777777777/merge-requests", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"mergeRequests":[{"id":"`+mergeTestRequest+`"`) {
		t.Fatalf("listing: %d %s", listed.Code, listed.Body.String())
	}

	rejected := mergeRoute(t, store, http.MethodPost, org+"/merge-requests/"+mergeTestRequest+"/reject", "")
	if rejected.Code != http.StatusOK || len(store.decided) != 1 || store.decided[0] != "rejected" {
		t.Fatalf("rejecting: %d %s, decided %v", rejected.Code, rejected.Body.String(), store.decided)
	}

	stale := &mergeRequestTestStore{}
	approved := mergeRoute(t, stale, http.MethodPost, org+"/merge-requests/"+mergeTestRequest+"/approve", `{"expectedHeadSha":"older"}`)
	if approved.Code != http.StatusConflict || len(stale.decided) != 0 {
		t.Fatalf("approving a changed PR: %d %s, decided %v; want 409 and nothing recorded", approved.Code, approved.Body.String(), stale.decided)
	}
}
