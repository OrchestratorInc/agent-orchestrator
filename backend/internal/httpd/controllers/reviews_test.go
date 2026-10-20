package controllers_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

type fakeReviewService struct {
	// triggeredHarness/config record the override the controller forwarded.
	triggeredHarness  domain.ReviewerHarness
	triggeredConfig   domain.AgentConfig
	triggeredMode     domain.ReviewerInterfaceMode
	triggeredRerun    bool
	triggerErr        error
	cancelErr         error
	trigger           reviewcore.TriggerResult
	cancel            reviewcore.CancelResult
	list              reviewcore.SessionReviews
	submitted         []reviewsvc.SubmittedReview
	activityID        string
	activitySignal    reviewsvc.ActivitySignal
	activityErr       error
	killed            bool
	teardown          bool
	restored          bool
	switchedHarness   domain.ReviewerHarness
	rereviewSession   domain.SessionID
	rereviewPRURL     string
	rereviewReviewer  string
	triggerRequest    reviewsvc.TriggerRequest
	autoInjectEnabled bool
	rereviewErr       error
	resolveSession    domain.SessionID
	resolvePRURL      string
	resolveCommentURL string
	resolveErr        error
	triggeredPRURL    string
}

func (*fakeReviewService) RecoverChatReviewers(context.Context) error { return nil }

func (f *fakeReviewService) runTrigger(
	_ context.Context,
	_ domain.SessionID,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
	prURL string,
) (reviewcore.TriggerResult, error) {
	f.triggeredHarness = harness
	f.triggeredConfig = config
	f.triggeredPRURL = prURL
	if f.triggerErr != nil {
		return reviewcore.TriggerResult{}, f.triggerErr
	}
	if f.trigger.ReviewerHandleID != "" || f.trigger.Run.ID != "" || f.trigger.Reviews != nil || f.trigger.CreatedRuns != nil {
		return f.trigger, nil
	}
	return reviewcore.TriggerResult{Run: domain.ReviewRun{ID: "run-1"}, Created: true}, nil
}

func (f *fakeReviewService) TriggerRequested(ctx context.Context, workerID domain.SessionID, req reviewsvc.TriggerRequest) (reviewsvc.TriggerOutcome, error) {
	f.triggerRequest = req
	f.triggeredRerun = req.Rerun
	f.triggeredMode = req.InterfaceMode
	res, err := f.runTrigger(ctx, workerID, req.Harness, req.Config, req.PRURL)
	if err != nil {
		return reviewsvc.TriggerOutcome{}, err
	}
	return reviewsvc.TriggerOutcome{TriggerResult: res, AutoInjectEnabled: req.EnableAutoInject && f.autoInjectEnabled}, nil
}

func (f *fakeReviewService) RequestRereview(_ context.Context, workerID domain.SessionID, prURL, reviewer string) error {
	f.rereviewSession = workerID
	f.rereviewPRURL = prURL
	f.rereviewReviewer = reviewer
	return f.rereviewErr
}

func (f *fakeReviewService) ResolveReviewComment(_ context.Context, workerID domain.SessionID, prURL, commentURL string) error {
	f.resolveSession = workerID
	f.resolvePRURL = prURL
	f.resolveCommentURL = commentURL
	return f.resolveErr
}

func (f *fakeReviewService) TriggerAuto(context.Context, domain.SessionID, domain.ReviewerHarness) (reviewcore.TriggerResult, error) {
	return reviewcore.TriggerResult{}, nil
}

func (f *fakeReviewService) Submit(_ context.Context, _ domain.SessionID, _ string, _ domain.ReviewVerdict, _ string, _ []domain.ReviewFinding) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func (f *fakeReviewService) ApplyReviewActivitySignal(_ context.Context, reviewSessionID string, signal reviewsvc.ActivitySignal) error {
	f.activityID = reviewSessionID
	f.activitySignal = signal
	return f.activityErr
}

func (f *fakeReviewService) Cancel(context.Context, domain.SessionID) (reviewcore.CancelResult, error) {
	if f.cancelErr != nil {
		return reviewcore.CancelResult{}, f.cancelErr
	}
	return f.cancel, nil
}

func (f *fakeReviewService) TerminateReviewer(context.Context, domain.SessionID, string) error {
	f.killed = true
	f.list.ReviewerHandleID = ""
	return nil
}

func (f *fakeReviewService) ArchiveReviewer(ctx context.Context, id domain.SessionID) error {
	f.list.ReviewerSurface = domain.ReviewerSurface{}
	return f.TerminateReviewer(ctx, id, "")
}

func (f *fakeReviewService) TeardownReviewerTerminal(context.Context, domain.SessionID) error {
	f.teardown = true
	f.list.ReviewerHandleID = ""
	return nil
}

func (f *fakeReviewService) RestoreReviewer(context.Context, domain.SessionID) error {
	f.restored = true
	if f.list.ReviewerHandleID == "" {
		f.list.ReviewerHandleID = "review-mer-1"
	}
	return nil
}

func (f *fakeReviewService) SwitchReviewer(_ context.Context, _ domain.SessionID, harness domain.ReviewerHarness, _ domain.AgentConfig) (reviewcore.SessionReviews, error) {
	f.switchedHarness = harness
	f.list.ReviewerHarness = harness
	if f.list.Runs == nil {
		f.list.Runs = []domain.ReviewRun{}
	}
	if f.list.Reviews == nil {
		f.list.Reviews = []reviewcore.PRReviewState{}
	}
	return f.list, nil
}

func (f *fakeReviewService) SubmitMany(_ context.Context, _ domain.SessionID, reviews []reviewsvc.SubmittedReview) ([]domain.ReviewRun, error) {
	f.submitted = append([]reviewsvc.SubmittedReview(nil), reviews...)
	runs := make([]domain.ReviewRun, 0, len(reviews))
	for _, review := range reviews {
		runs = append(runs, domain.ReviewRun{ID: review.RunID, Verdict: review.Verdict, Body: review.Body, Findings: review.Findings})
	}
	return runs, nil
}

func (f *fakeReviewService) List(context.Context, domain.SessionID) (reviewcore.SessionReviews, error) {
	return f.list, nil
}

func newReviewTestServer(t *testing.T, svc reviewsvc.Manager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reviews: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReviewsTrigger_MissingReviewerBinaryReturns422WithCause(t *testing.T) {
	err := fmt.Errorf("launch reviewer: reviewer command: claude: %w", ports.ErrAgentBinaryNotFound)
	srv := newReviewTestServer(t, &fakeReviewService{triggerErr: err})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "REVIEWER_BINARY_NOT_FOUND")

	var got errorBody
	mustJSON(t, body, &got)
	if !strings.Contains(got.Message, "claude") || !strings.Contains(got.Message, ports.ErrAgentBinaryNotFound.Error()) {
		t.Fatalf("message = %q, want reviewer binary cause", got.Message)
	}
}

func TestReviewsTrigger_UnauthenticatedReviewerReturns409(t *testing.T) {
	srv := newReviewTestServer(t, &fakeReviewService{triggerErr: fmt.Errorf("reviewer harness %q: %w", "claude-code", ports.ErrChatAuthRequired)})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusConflict, "REVIEWER_AUTH_REQUIRED")

	var got errorBody
	mustJSON(t, body, &got)
	if got.Message != "The reviewer agent is installed but not authenticated" {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestReviewsTrigger_PassesPRURLThrough(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", `{"prUrl":"https://github.com/o/r/pull/2"}`)
	if svc.triggeredPRURL != "https://github.com/o/r/pull/2" {
		t.Fatalf("trigger prUrl = %q, want the request body value", svc.triggeredPRURL)
	}
}

func TestReviewActivityPersistsReviewerNativeSessionID(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/reviews/review-1/activity", `{"event":"session-start","agentSessionId":"native-review-1","launchId":"launch-7"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.activityID != "review-1" {
		t.Fatalf("activity id = %q, want review-1", svc.activityID)
	}
	if svc.activitySignal.Event != "session-start" || svc.activitySignal.AgentSessionID != "native-review-1" || svc.activitySignal.LaunchID != "launch-7" {
		t.Fatalf("activity signal = %+v", svc.activitySignal)
	}
}

func TestReviewActivityPersistsReviewerState(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/reviews/review-1/activity", `{"event":"stop","state":"idle"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.activitySignal.State != domain.ActivityIdle {
		t.Fatalf("activity signal = %+v", svc.activitySignal)
	}
}

func TestReviewActivityIgnoresUnknownStateWithoutMetadata(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/reviews/review-1/activity", `{"state":"busy"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.activityID != "" {
		t.Fatalf("activity should be ignored, got id=%q signal=%+v", svc.activityID, svc.activitySignal)
	}
}

func TestReviewsListIncludesReviewStates(t *testing.T) {
	srv := newReviewTestServer(t, &fakeReviewService{list: reviewcore.SessionReviews{
		ReviewerHandleID:      "review-mer-1",
		ReviewerHarness:       domain.ReviewerCodex,
		ReviewerActivityState: domain.ActivityIdle,
		Runs:                  []domain.ReviewRun{{ID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", AutoInjectReview: false}},
		Reviews:               []reviewcore.PRReviewState{{PRURL: "https://github.com/o/r/pull/1", PRNumber: 1, TargetSHA: "sha1", Status: reviewcore.ReviewStateUpToDate}},
	}})

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/reviews", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"reviews"`) || !strings.Contains(string(body), `"up_to_date"`) || !strings.Contains(string(body), `"reviewerHandleId":"review-mer-1"`) || !strings.Contains(string(body), `"reviewerHarness":"codex"`) || !strings.Contains(string(body), `"reviewerActivityState":"idle"`) {
		t.Fatalf("body missing review states/handle: %s", body)
	}
	if !strings.Contains(string(body), `"autoInjectReview":false`) {
		t.Fatalf("body missing stored AO injection decision: %s", body)
	}
	if strings.Contains(string(body), `"items"`) || strings.Contains(string(body), `"reviewItems"`) || strings.Contains(string(body), `"reviewRuns"`) {
		t.Fatalf("body contains deprecated review item aliases: %s", body)
	}
}

func TestReviewsTriggerIncludesBatchFields(t *testing.T) {
	run1 := domain.ReviewRun{ID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"}
	run2 := domain.ReviewRun{ID: "run-2", PRURL: "https://github.com/o/r/pull/2", TargetSHA: "sha2"}
	srv := newReviewTestServer(t, &fakeReviewService{trigger: reviewcore.TriggerResult{
		Run:              run1,
		ReviewerHandleID: "review-mer-1",
		Created:          true,
		CreatedRuns:      []domain.ReviewRun{run1, run2},
		Runs:             []domain.ReviewRun{run1, run2},
		Reviews: []reviewcore.PRReviewState{
			{PRURL: run1.PRURL, PRNumber: 1, TargetSHA: run1.TargetSHA, Status: reviewcore.ReviewStateRunning, LatestRun: &run1},
			{PRURL: run2.PRURL, PRNumber: 2, TargetSHA: run2.TargetSHA, Status: reviewcore.ReviewStateRunning, LatestRun: &run2},
		},
	}})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", "")
	assertJSON(t, headers)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, body)
	}
	for _, want := range []string{`"reviews"`, `"runs"`, `"running"`, `"run-1"`, `"run-2"`, `"reviewerHandleId":"review-mer-1"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	for _, unwanted := range []string{`"reviewItems"`, `"items"`, `"createdReviews"`, `"createdRuns"`, `"reviewRuns"`, `"review":`} {
		if strings.Contains(string(body), unwanted) {
			t.Fatalf("body contains deprecated field %s: %s", unwanted, body)
		}
	}
}

func TestReviewsTriggerForwardsRequestedInterfaceMode(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", `{"harness":"codex","interfaceMode":"tui"}`)
	if status != http.StatusCreated {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.triggeredHarness != domain.ReviewerCodex || svc.triggeredMode != domain.ReviewerInterfaceTUI {
		t.Fatalf("triggered harness=%q mode=%q", svc.triggeredHarness, svc.triggeredMode)
	}
}

func TestReviewsResolveCommentForwardsPRAndComment(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/comments/resolve", `{"pullRequestUrl":"https://github.com/o/r/pull/1","commentUrl":"https://github.com/o/r/pull/1#discussion_r1"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.resolveSession != "mer-1" || svc.resolvePRURL != "https://github.com/o/r/pull/1" || svc.resolveCommentURL != "https://github.com/o/r/pull/1#discussion_r1" {
		t.Fatalf("request = session %q pr %q comment %q", svc.resolveSession, svc.resolvePRURL, svc.resolveCommentURL)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("body missing ok: %s", body)
	}
}

func TestReviewsRerequestForwardsReviewerAndPR(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/rerequest", `{"reviewerId":"prateek","pullRequestUrl":"https://github.com/o/r/pull/1"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.rereviewSession != "mer-1" || svc.rereviewReviewer != "prateek" || svc.rereviewPRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("request = session %q reviewer %q pr %q", svc.rereviewSession, svc.rereviewReviewer, svc.rereviewPRURL)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("body missing ok: %s", body)
	}
}

func TestReviewsRerequestInvalidJSON(t *testing.T) {
	srv := newReviewTestServer(t, &fakeReviewService{})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/rerequest", `{`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

func TestReviewsCancelIncludesReviewStates(t *testing.T) {
	srv := newReviewTestServer(t, &fakeReviewService{cancel: reviewcore.CancelResult{
		ReviewerHandleID: "review-mer-1",
		Reviews: []reviewcore.PRReviewState{
			{PRURL: "https://github.com/o/r/pull/1", PRNumber: 1, TargetSHA: "sha1", Status: reviewcore.ReviewStateNeedsReview},
		},
	}})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/cancel", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	for _, want := range []string{`"reviews"`, `"needs_review"`, `"reviewerHandleId":"review-mer-1"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestReviewsKillArchivesReviewerSurface(t *testing.T) {
	svc := &fakeReviewService{list: reviewcore.SessionReviews{
		ReviewerHandleID: "review-mer-1",
		ReviewerHarness:  domain.ReviewerCodex,
		ReviewerSurface:  domain.ReviewerSurface{Mode: domain.ReviewerInterfaceChat, ReviewID: "review-1", Harness: domain.ReviewerCodex},
		Reviews:          []reviewcore.PRReviewState{{PRURL: "https://github.com/o/r/pull/1", PRNumber: 1, TargetSHA: "sha1", Status: reviewcore.ReviewStateNeedsReview}},
		Runs:             []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Harness: domain.ReviewerCodex}},
	}}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/kill", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !svc.killed {
		t.Fatal("ArchiveReviewer was not called")
	}
	for _, want := range []string{`"reviewerHandleId":""`, `"reviews"`, `"runs"`, `"run-1"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}

	refreshed, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/reviews", "")
	if status != http.StatusOK || strings.Contains(string(refreshed), `"reviewerSurface"`) || !strings.Contains(string(refreshed), `"run-1"`) {
		t.Fatalf("archived refresh: %d %s", status, refreshed)
	}
}

func TestReviewsRestoreReturnsReviewerHandle(t *testing.T) {
	svc := &fakeReviewService{list: reviewcore.SessionReviews{
		ReviewerHarness: domain.ReviewerCodex,
		Reviews:         []reviewcore.PRReviewState{{PRURL: "https://github.com/o/r/pull/1", PRNumber: 1, TargetSHA: "sha1", Status: reviewcore.ReviewStateNeedsReview}},
		Runs:            []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Harness: domain.ReviewerCodex}},
	}}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/restore", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !svc.restored {
		t.Fatal("RestoreReviewer was not called")
	}
	for _, want := range []string{`"reviewerHandleId":"review-mer-1"`, `"reviews"`, `"runs"`, `"run-1"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestReviewsSwitchReturnsAuthoritativeReviewState(t *testing.T) {
	svc := &fakeReviewService{list: reviewcore.SessionReviews{
		ReviewerHandleID: "review-mer-2",
		Reviews:          []reviewcore.PRReviewState{{PRURL: "https://github.com/o/r/pull/1", PRNumber: 1, TargetSHA: "sha1", Status: reviewcore.ReviewStateNeedsReview}},
		Runs:             []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode}},
	}}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/switch", `{"harness":"claude-code"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.switchedHarness != domain.ReviewerClaudeCode {
		t.Fatalf("switched harness = %q, want claude-code", svc.switchedHarness)
	}
	for _, want := range []string{`"reviewerHandleId":"review-mer-2"`, `"reviewerHarness":"claude-code"`, `"reviews"`, `"runs"`, `"run-1"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestReviewsSubmitCarriesFindingsAndRejectsObsoleteInputs(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/submit", `{"runId":"run-1","verdict":"changes_requested","body":"fix auth","comments":[{"path":"src/auth.go","line":42,"body":"Missing authorization check."}]}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if len(svc.submitted) != 1 || svc.submitted[0].RunID != "run-1" || len(svc.submitted[0].Findings) != 1 || svc.submitted[0].Findings[0].Path != "src/auth.go" || svc.submitted[0].Findings[0].Line != 42 {
		t.Fatalf("submitted = %+v", svc.submitted)
	}
	if !strings.Contains(string(body), `"run-1"`) {
		t.Fatalf("body missing run id: %s", body)
	}

	// A caller-supplied GitHub review id is an obsolete input: AO owns
	// publication, so ids are outputs and must fail clearly, never
	// silently succeed. Batched results (reviews: [...]) are valid now.
	for name, payload := range map[string]string{
		"githubReviewId": `{"runId":"run-1","verdict":"approved","body":"ok","githubReviewId":"101"}`,
	} {
		body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/submit", payload)
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d body=%s, want 422", name, status, body)
		}
		if !strings.Contains(string(body), "REVIEW_INPUT_OBSOLETE") {
			t.Fatalf("%s: body missing obsolete code: %s", name, body)
		}
	}
}

func TestReviewsTriggerForwardsExplicitRerun(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newReviewTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", `{"rerun":true,"harness":"codex","interfaceMode":"chat","agentConfig":{"model":"test-model"}}`)
	if status != http.StatusCreated || !svc.triggeredRerun || svc.triggeredHarness != domain.ReviewerCodex || svc.triggeredMode != domain.ReviewerInterfaceChat || svc.triggeredConfig.Model != "test-model" {
		t.Fatalf("forwarding: status=%d service=%+v body=%s", status, svc, body)
	}
}

func TestReviewsTriggerForwardsAgentPolicyAndReportsAutoInject(t *testing.T) {
	svc := &fakeReviewService{autoInjectEnabled: true}
	srv := newReviewTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", `{"harness":"codex","agentConfig":{"model":"gpt-5.5"},"source":"agent","rejectReviewedHead":true,"enableAutoInject":true,"prUrl":"https://github.com/o/r/pull/3"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, body)
	}
	req := svc.triggerRequest
	if req.Harness != domain.ReviewerCodex || req.Config.Model != "gpt-5.5" || req.Source != domain.ReviewTriggerAgent || !req.RejectReviewedHead || req.Rerun || !req.EnableAutoInject || req.PRURL != "https://github.com/o/r/pull/3" {
		t.Fatalf("forwarded request = %+v", req)
	}
	var got controllers.TriggerReviewResponse
	mustJSON(t, body, &got)
	if !got.Created || !got.AutoInjectEnabled {
		t.Fatalf("response = %+v, want created with auto-inject enabled", got)
	}
}

func TestReviewsTriggerMapsSameCommitConflictsTo409(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("%w: codex is already reviewing PR #1 head abc", reviewcore.ErrReviewAlreadyRunning), "REVIEW_ALREADY_RUNNING"},
		{fmt.Errorf("%w: PR #1 head abc was already reviewed (approved); push new commits, or pass --rerun", reviewcore.ErrHeadAlreadyReviewed), "REVIEW_HEAD_ALREADY_REVIEWED"},
		{fmt.Errorf("%w: PR #1 head abc belongs to active session mer-2", reviewcore.ErrPROwnedElsewhere), "REVIEW_PR_OWNED_BY_OTHER_SESSION"},
	} {
		srv := newReviewTestServer(t, &fakeReviewService{triggerErr: tc.err})
		body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", `{"rejectReviewedHead":true}`)
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusConflict, tc.code)
		var got errorBody
		mustJSON(t, body, &got)
		if !strings.Contains(got.Message, "PR #1 head abc") {
			t.Fatalf("message = %q, want the actionable explanation", got.Message)
		}
	}
}

func TestReviewsListIncludesEveryActiveReviewer(t *testing.T) {
	svc := &fakeReviewService{list: reviewcore.SessionReviews{
		ActiveReviewers: []domain.ReviewerSurface{
			{Mode: domain.ReviewerInterfaceTUI, ReviewID: "rev-claude", Harness: domain.ReviewerClaudeCode, HandleID: "claude-pane"},
			{Mode: domain.ReviewerInterfaceTUI, ReviewID: "rev-codex", Harness: domain.ReviewerCodex, HandleID: "codex-pane"},
		},
	}}
	srv := newReviewTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/reviews", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	var got controllers.ListReviewsResponse
	mustJSON(t, body, &got)
	if len(got.ActiveReviewers) != 2 || got.ActiveReviewers[1].HandleID != "codex-pane" {
		t.Fatalf("activeReviewers = %+v, want both reviewers", got.ActiveReviewers)
	}

	empty := newReviewTestServer(t, &fakeReviewService{})
	body, _, _ = doRequest(t, empty, "GET", "/api/v1/sessions/mer-1/reviews", "")
	if !strings.Contains(string(body), `"activeReviewers":[]`) {
		t.Fatalf("body = %s, want an empty activeReviewers array, never null", body)
	}
}
