package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type stubWorkerReviewStore struct {
	Store
	reviewer  string
	available []string
	prs       []domain.PullRequest
	org       string
	session   string
}

func (s *stubWorkerReviewStore) WorkerReviewTarget(_ context.Context, orgID, sessionID string) (string, []string, []domain.PullRequest, error) {
	s.org, s.session = orgID, sessionID
	return s.reviewer, s.available, s.prs, nil
}

// stubReviewRuns records the runs the shared review lifecycle creates.
type stubReviewRuns struct {
	githubapp.Store
	running map[string]bool
	created []string
}

func (s *stubReviewRuns) CreateReviewRun(_ context.Context, _, pullRequestID, _, _, harness, triggerSource string) (domain.ReviewRun, bool, error) {
	if s.running[pullRequestID] {
		return domain.ReviewRun{ID: "existing-" + pullRequestID}, false, nil
	}
	s.created = append(s.created, pullRequestID+":"+harness+":"+triggerSource)
	return domain.ReviewRun{ID: "run-" + pullRequestID}, true, nil
}

func (s *stubReviewRuns) OpenReviewTerminal(context.Context, string, string, string, string, string) (string, error) {
	return "terminal-1", nil
}

func workerReviewServer(store Store, runs *stubReviewRuns) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{store: store, logger: logger, reviewService: githubapp.NewReviewService(runs, logger)}
}

func TestWorkerTriggerReviewsStartsTheSessionsOpenPullRequests(t *testing.T) {
	store := &stubWorkerReviewStore{reviewer: "codex", available: []string{"claude-code", "codex"}, prs: []domain.PullRequest{
		{ID: "open", Number: 4, URL: "https://github.test/o/r/pull/4", State: contract.PRStateOpen, HeadSHA: "sha-4"},
		{ID: "already", Number: 5, URL: "https://github.test/o/r/pull/5", State: contract.PRStateOpen, HeadSHA: "sha-5"},
		{ID: "draft", Number: 6, State: contract.PRStateOpen, Draft: true, HeadSHA: "sha-6"},
		{ID: "merged", Number: 7, State: contract.PRStateMerged, HeadSHA: "sha-7"},
	}}
	runs := &stubReviewRuns{running: map[string]bool{"already": true}}
	w := httptest.NewRecorder()
	workerReviewServer(store, runs).workerTriggerReviews(w, workerRequest(t, http.MethodPost, "/worker/reviews/trigger", "", "worker:git"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	if store.org != testOrgID || store.session != testOrchestratorID {
		t.Fatalf("resolved %s/%s, want the calling worker's own session", store.org, store.session)
	}
	if len(runs.created) != 1 || runs.created[0] != "open:codex:manual" {
		t.Fatalf("created runs = %v", runs.created)
	}
	var response worker.TriggerReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := []worker.TriggeredReview{{Number: 4, URL: "https://github.test/o/r/pull/4", Started: true}, {Number: 5, URL: "https://github.test/o/r/pull/5", Started: false}}
	if len(response.Reviews) != len(want) || response.Reviews[0] != want[0] || response.Reviews[1] != want[1] {
		t.Fatalf("reviews = %+v, want %+v", response.Reviews, want)
	}
}

func TestWorkerTriggerReviewsRejectsAnUnconnectedReviewer(t *testing.T) {
	store := &stubWorkerReviewStore{reviewer: "cursor", available: []string{"codex"}, prs: []domain.PullRequest{
		{ID: "open", Number: 4, State: contract.PRStateOpen, HeadSHA: "sha-4"},
	}}
	runs := &stubReviewRuns{}
	w := httptest.NewRecorder()
	workerReviewServer(store, runs).workerTriggerReviews(w, workerRequest(t, http.MethodPost, "/worker/reviews/trigger", "", "worker:git"))
	if w.Code != http.StatusUnprocessableEntity || len(runs.created) != 0 {
		t.Fatalf("status = %d created=%v", w.Code, runs.created)
	}
}

func TestWorkerTriggerReviewsRequiresGitScope(t *testing.T) {
	runs := &stubReviewRuns{}
	w := httptest.NewRecorder()
	workerReviewServer(&stubWorkerReviewStore{}, runs).workerTriggerReviews(w, workerRequest(t, http.MethodPost, "/worker/reviews/trigger", "", "worker:connect"))
	if w.Code != http.StatusForbidden || len(runs.created) != 0 {
		t.Fatalf("status = %d created=%v", w.Code, runs.created)
	}
}
