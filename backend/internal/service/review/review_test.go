package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

type fakeStore struct {
	run                     domain.ReviewRun
	ok                      bool
	review                  domain.Review
	reviewOK                bool
	batchRuns               []domain.ReviewRun
	prs                     []domain.PullRequest
	prReviews               map[string][]domain.PullRequestReview
	prComments              map[string][]domain.PullRequestComment
	sessionAutoInjectReview *bool
	autoInjectSets          []bool

	updateCalls        int
	activityUpdates    int
	resolvedCommentIDs []string
}

type fakeNotificationSink struct {
	intents []ports.NotificationIntent
}

func (f *fakeNotificationSink) Notify(_ context.Context, intent ports.NotificationIntent) error {
	f.intents = append(f.intents, intent)
	return nil
}

func (f *fakeStore) GetReviewByID(_ context.Context, id string) (domain.Review, bool, error) {
	if f.reviewOK && f.review.ID == id {
		return f.review, true, nil
	}
	return domain.Review{}, false, nil
}

func (f *fakeStore) UpdateReviewActivity(_ context.Context, id string, state domain.ActivityState, agentSessionID, launchID string) (bool, error) {
	if !f.reviewOK || f.review.ID != id {
		return false, nil
	}
	switch {
	case f.review.ReviewerLaunchID != "" && launchID != f.review.ReviewerLaunchID:
		return false, nil
	case f.review.ReviewerLaunchID == "" && launchID != "":
		return false, nil
	}
	f.activityUpdates++
	if agentSessionID != "" {
		f.review.AgentSessionID = agentSessionID
	}
	if state != "" {
		f.review.ReviewerActivityState = state
	}
	if launchID != "" {
		f.review.ReviewerLaunchID = launchID
	}
	return true, nil
}

func (f *fakeStore) GetReviewRun(_ context.Context, id string) (domain.ReviewRun, bool, error) {
	for _, run := range f.batchRuns {
		if run.ID == id {
			return run, true, nil
		}
	}
	if f.ok && f.run.ID == id {
		return f.run, true, nil
	}
	return domain.ReviewRun{}, false, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	enabled := true
	if f.sessionAutoInjectReview != nil {
		enabled = *f.sessionAutoInjectReview
	}
	return domain.SessionRecord{ID: id, AutoInjectReview: enabled}, true, nil
}

func (f *fakeStore) SetSessionAutoInjectReview(_ context.Context, _ domain.SessionID, autoInject bool, _ time.Time) (bool, error) {
	f.autoInjectSets = append(f.autoInjectSets, autoInject)
	f.sessionAutoInjectReview = &autoInject
	return true, nil
}

func (f *fakeStore) UpdateReviewRunResult(_ context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string, autoInjectReview bool) (bool, error) {
	for i := range f.batchRuns {
		if f.batchRuns[i].ID == id {
			if f.batchRuns[i].Status != domain.ReviewRunRunning {
				return false, nil
			}
			f.updateCalls++
			f.batchRuns[i].Status = status
			f.batchRuns[i].Verdict = verdict
			f.batchRuns[i].Body = body
			f.batchRuns[i].GithubReviewID = githubReviewID
			f.batchRuns[i].AutoInjectReview = autoInjectReview
			if f.run.ID == id {
				f.run = f.batchRuns[i]
			}
			return true, nil
		}
	}
	if f.run.Status != domain.ReviewRunRunning {
		return false, nil
	}
	f.updateCalls++
	f.run.Status = status
	f.run.Verdict = verdict
	f.run.Body = body
	f.run.GithubReviewID = githubReviewID
	f.run.AutoInjectReview = autoInjectReview
	return true, nil
}

func (f *fakeStore) ListReviewRunsByBatch(context.Context, domain.SessionID, string) ([]domain.ReviewRun, error) {
	out := append([]domain.ReviewRun(nil), f.batchRuns...)
	return out, nil
}

func (f *fakeStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	out := append([]domain.PullRequest(nil), f.prs...)
	return out, nil
}

func (f *fakeStore) ListPRReviews(_ context.Context, prURL string) ([]domain.PullRequestReview, error) {
	out := append([]domain.PullRequestReview(nil), f.prReviews[prURL]...)
	return out, nil
}

func (f *fakeStore) ListPRComments(_ context.Context, prURL string) ([]domain.PullRequestComment, error) {
	out := append([]domain.PullRequestComment(nil), f.prComments[prURL]...)
	return out, nil
}

func (f *fakeStore) MarkPRCommentResolved(_ context.Context, prURL, commentID string) (bool, error) {
	f.resolvedCommentIDs = append(f.resolvedCommentIDs, commentID)
	comments := f.prComments[prURL]
	for i := range comments {
		if comments[i].ID == commentID {
			comments[i].Resolved = true
			f.prComments[prURL] = comments
			return true, nil
		}
	}
	return false, nil
}

type fakeReviewResolver struct {
	request ports.SCMReviewResolveRequest
	err     error
}

func (f *fakeReviewResolver) ResolveReviewThread(_ context.Context, request ports.SCMReviewResolveRequest) error {
	f.request = request
	return f.err
}

type fakeReviewRequester struct {
	request ports.SCMReviewRequest
	err     error
}

func (f *fakeReviewRequester) RequestReview(_ context.Context, request ports.SCMReviewRequest) error {
	f.request = request
	return f.err
}

func TestResolveReviewCommentResolvesTrackedThread(t *testing.T) {
	prURL := "https://github.com/acme/widget/pull/7"
	commentURL := "https://github.com/acme/widget/pull/7#discussion_r1"
	store := &fakeStore{
		prs: []domain.PullRequest{{URL: prURL, Number: 7, Provider: "github", Repo: "acme/widget"}},
		prComments: map[string][]domain.PullRequestComment{
			prURL: {{ThreadID: "thread-1", ID: "comment-1", URL: commentURL}},
		},
	}
	resolver := &fakeReviewResolver{}
	svc := New(nil, store, WithReviewResolver(resolver))

	if err := svc.ResolveReviewComment(context.Background(), "mer-1", prURL, commentURL); err != nil {
		t.Fatal(err)
	}
	if resolver.request.ThreadID != "thread-1" || resolver.request.PR.Number != 7 {
		t.Fatalf("request = %#v", resolver.request)
	}
	if got := store.resolvedCommentIDs; len(got) != 1 || got[0] != "comment-1" {
		t.Fatalf("resolved comment ids = %#v", got)
	}
	if !store.prComments[prURL][0].Resolved {
		t.Fatalf("comment was not marked resolved: %#v", store.prComments[prURL][0])
	}
}

func TestResolveReviewCommentDoesNotPersistWhenProviderFails(t *testing.T) {
	prURL := "https://github.com/acme/widget/pull/7"
	commentURL := "https://github.com/acme/widget/pull/7#discussion_r1"
	store := &fakeStore{
		prs: []domain.PullRequest{{URL: prURL, Number: 7, Provider: "github", Repo: "acme/widget"}},
		prComments: map[string][]domain.PullRequestComment{
			prURL: {{ThreadID: "thread-1", ID: "comment-1", URL: commentURL}},
		},
	}
	resolver := &fakeReviewResolver{err: errors.New("provider down")}
	svc := New(nil, store, WithReviewResolver(resolver))

	if err := svc.ResolveReviewComment(context.Background(), "mer-1", prURL, commentURL); err == nil {
		t.Fatal("ResolveReviewComment error = nil, want provider failure")
	}
	if len(store.resolvedCommentIDs) != 0 {
		t.Fatalf("resolved comment ids = %#v, want none", store.resolvedCommentIDs)
	}
	if store.prComments[prURL][0].Resolved {
		t.Fatalf("comment was marked resolved after provider failure")
	}
}

func TestRequestRereviewRequestsReviewerForTrackedPR(t *testing.T) {
	prURL := "https://github.com/acme/widget/pull/7"
	store := &fakeStore{
		prs: []domain.PullRequest{{
			URL:      prURL,
			Number:   7,
			Provider: "github",
			Host:     "github.com",
			Repo:     "acme/widget",
		}},
		prReviews: map[string][]domain.PullRequestReview{
			prURL: {{Author: "prateek"}},
		},
	}
	requester := &fakeReviewRequester{}
	svc := New(nil, store, WithReviewRequester(requester))

	if err := svc.RequestRereview(context.Background(), "mer-1", prURL, "@prateek"); err != nil {
		t.Fatal(err)
	}
	if requester.request.Reviewer != "prateek" || requester.request.PR.Number != 7 || requester.request.PR.Repo.Owner != "acme" || requester.request.PR.Repo.Name != "widget" {
		t.Fatalf("request = %#v", requester.request)
	}
}

func TestRequestRereviewRejectsUnknownReviewer(t *testing.T) {
	prURL := "https://github.com/acme/widget/pull/7"
	store := &fakeStore{
		prs:       []domain.PullRequest{{URL: prURL, Number: 7, Provider: "github", Repo: "acme/widget"}},
		prReviews: map[string][]domain.PullRequestReview{prURL: {{Author: "someone-else"}}},
	}
	svc := New(nil, store, WithReviewRequester(&fakeReviewRequester{}))

	if err := svc.RequestRereview(context.Background(), "mer-1", prURL, "prateek"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

// AO never sends the worker a separate verdict message: the reviewer's posted
// review reaches the worker through the SCM observer like any other review. A
// submitted result is recorded and stays complete.
func TestSubmitRecordsTheResultWithoutASeparateDelivery(t *testing.T) {
	st := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
	}
	svc := New(nil, st)

	for _, verdict := range []domain.ReviewVerdict{domain.VerdictChangesRequested, domain.VerdictApproved} {
		st.run.Status, st.run.Verdict, st.run.Body = domain.ReviewRunRunning, domain.VerdictNone, ""
		run, err := svc.Submit(context.Background(), "mer-1", "run-1", verdict, "body", "987")
		if err != nil {
			t.Fatalf("%s: Submit: %v", verdict, err)
		}
		if run.Status != domain.ReviewRunComplete || run.DeliveredAt != nil {
			t.Fatalf("%s: run = %+v, want complete and never stamped delivered", verdict, run)
		}
	}
}

func TestApplyReviewActivitySignalPersistsNativeReviewerSessionID(t *testing.T) {
	st := &fakeStore{
		reviewOK: true,
		review:   domain.Review{ID: "review-1", SessionID: "worker-1", Harness: domain.ReviewerOpenCode, AgentSessionID: "old-native"},
	}
	svc := New(nil, st)

	if err := svc.ApplyReviewActivitySignal(context.Background(), "review-1", ActivitySignal{
		Event:          "session-start",
		AgentSessionID: "opencode-native-2",
	}); err != nil {
		t.Fatalf("ApplyReviewActivitySignal: %v", err)
	}
	if st.activityUpdates != 1 || st.review.AgentSessionID != "opencode-native-2" {
		t.Fatalf("activity update calls=%d review=%+v", st.activityUpdates, st.review)
	}
	if st.review.SessionID != "worker-1" {
		t.Fatalf("worker session id changed: %+v", st.review)
	}
}

func TestApplyReviewActivitySignalPersistsReviewerActivityState(t *testing.T) {
	st := &fakeStore{
		reviewOK: true,
		review:   domain.Review{ID: "review-1", SessionID: "worker-1", Harness: domain.ReviewerOpenCode},
	}
	svc := New(nil, st)

	if err := svc.ApplyReviewActivitySignal(context.Background(), "review-1", ActivitySignal{
		Event: "stop",
		State: domain.ActivityIdle,
	}); err != nil {
		t.Fatalf("ApplyReviewActivitySignal: %v", err)
	}
	if st.activityUpdates != 1 || st.review.ReviewerActivityState != domain.ActivityIdle {
		t.Fatalf("activity update calls=%d review=%+v", st.activityUpdates, st.review)
	}
}

func TestApplyReviewActivitySignalIgnoresStaleLaunchGeneration(t *testing.T) {
	st := &fakeStore{
		reviewOK: true,
		review: domain.Review{
			ID:                    "review-1",
			SessionID:             "worker-1",
			Harness:               domain.ReviewerOpenCode,
			ReviewerLaunchID:      "launch-current",
			ReviewerActivityState: domain.ActivityActive,
		},
	}
	svc := New(nil, st)

	if err := svc.ApplyReviewActivitySignal(context.Background(), "review-1", ActivitySignal{
		Event:    "stop",
		State:    domain.ActivityIdle,
		LaunchID: "launch-stale",
	}); err != nil {
		t.Fatalf("ApplyReviewActivitySignal stale generation: %v", err)
	}
	if st.activityUpdates != 0 {
		t.Fatalf("stale generation performed update calls=%d review=%+v", st.activityUpdates, st.review)
	}
	if st.review.ReviewerActivityState != domain.ActivityActive || st.review.ReviewerLaunchID != "launch-current" {
		t.Fatalf("stale generation changed persisted review = %+v", st.review)
	}
}

func TestApplyReviewActivitySignalIgnoresMissingLaunchIDAfterGenerationClaimed(t *testing.T) {
	st := &fakeStore{
		reviewOK: true,
		review: domain.Review{
			ID:                    "review-1",
			SessionID:             "worker-1",
			Harness:               domain.ReviewerOpenCode,
			ReviewerLaunchID:      "launch-current",
			ReviewerActivityState: domain.ActivityActive,
			AgentSessionID:        "native-current",
		},
	}
	svc := New(nil, st)

	if err := svc.ApplyReviewActivitySignal(context.Background(), "review-1", ActivitySignal{
		Event:          "session-start",
		AgentSessionID: "legacy-native",
	}); err != nil {
		t.Fatalf("ApplyReviewActivitySignal missing launch id: %v", err)
	}
	if st.activityUpdates != 0 {
		t.Fatalf("missing launch id performed update calls=%d review=%+v", st.activityUpdates, st.review)
	}
	if st.review.ReviewerActivityState != domain.ActivityActive || st.review.ReviewerLaunchID != "launch-current" || st.review.AgentSessionID != "native-current" {
		t.Fatalf("missing launch id changed persisted review = %+v", st.review)
	}
}

func TestApplyReviewActivitySignalRequiresExistingReviewSession(t *testing.T) {
	svc := New(nil, &fakeStore{})

	err := svc.ApplyReviewActivitySignal(context.Background(), "missing-review", ActivitySignal{AgentSessionID: "native-1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSubmitSnapshotsDisabledPolicyAndNeverDeliversOnRetry(t *testing.T) {
	disabled := false
	st := &fakeStore{
		ok:                      true,
		sessionAutoInjectReview: &disabled,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
	}
	svc := New(nil, st)

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.ReviewRunComplete || run.AutoInjectReview {
		t.Fatalf("disabled review = %+v", run)
	}

	enabled := true
	st.sessionAutoInjectReview = &enabled
	run, err = svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.ReviewRunComplete || run.AutoInjectReview {
		t.Fatalf("retry rewrote disabled review = %+v", run)
	}
}

func TestSubmitEmitsIdempotentReviewResultNotification(t *testing.T) {
	st := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "https://github.com/acme/app/pull/42", Status: domain.ReviewRunRunning},
		prs: []domain.PullRequest{{URL: "https://github.com/acme/app/pull/42", Number: 42, Title: "Fix checkout"}},
	}
	sink := &fakeNotificationSink{}
	svc := New(nil, st, WithNotificationSink(sink))

	for range 2 {
		if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "looks good", "987"); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if len(sink.intents) != 2 {
		t.Fatalf("notification attempts = %d, want 2 so durable source-key dedupe can recover retries", len(sink.intents))
	}
	for _, intent := range sink.intents {
		if intent.Type != domain.NotificationReviewCompleted || intent.SourceKey != "review_run:run-1" || intent.PRNumber != 42 || intent.PRTitle != "Fix checkout" {
			t.Fatalf("notification intent = %+v", intent)
		}
	}
}

func TestSubmitEmitsChangesRequestedNotification(t *testing.T) {
	st := &fakeStore{ok: true, run: domain.ReviewRun{
		ID: "run-2", SessionID: "mer-1", PRURL: "https://github.com/acme/app/pull/43", Status: domain.ReviewRunRunning,
	}}
	sink := &fakeNotificationSink{}
	svc := New(nil, st, WithNotificationSink(sink))

	if _, err := svc.Submit(context.Background(), "mer-1", "run-2", domain.VerdictChangesRequested, "fix it", ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(sink.intents) != 1 || sink.intents[0].Type != domain.NotificationReviewChangesRequested {
		t.Fatalf("notification intents = %+v", sink.intents)
	}
}

func TestSubmitManySkipsSupersededRunAndRecordsSiblings(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	st := &fakeStore{
		ok: true,
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
			// A newer-commit trigger superseded run-2 while the reviewer was still
			// working on the original batch.
			{ID: "run-2", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2", Status: domain.ReviewRunFailed},
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}, {URL: "pr2", HeadSHA: "sha2-new"}},
	}
	svc := New(nil, st, WithClock(func() time.Time { return now }))

	runs, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{
		{RunID: "run-1", Verdict: domain.VerdictChangesRequested, Body: "fix pr1"},
		{RunID: "run-2", Verdict: domain.VerdictChangesRequested, Body: "fix pr2"},
	})
	if err != nil {
		t.Fatalf("SubmitMany must record valid siblings when one run was superseded: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" || runs[0].Status != domain.ReviewRunComplete {
		t.Fatalf("want only run-1 recorded, got %+v", runs)
	}
}

func TestSubmitManyRejectsOnlySupersededRuns(t *testing.T) {
	st := &fakeStore{
		ok: true,
		batchRuns: []domain.ReviewRun{{
			ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunCancelled,
		}},
	}
	svc := New(nil, st)

	if _, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{{
		RunID: "run-1", Verdict: domain.VerdictApproved,
	}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	} else if !strings.Contains(err.Error(), "superseded: run-1") {
		t.Fatalf("err = %v, want rejected run id", err)
	}
}

func TestSubmitCompletedRetryRejectsDifferentRecordedFields(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		githubReviewID string
	}{
		{name: "different body", body: "different", githubReviewID: "987"},
		{name: "different review id", body: "fix it", githubReviewID: "654"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &fakeStore{ok: true, run: domain.ReviewRun{
				ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1",
				Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested,
				Body: "fix it", GithubReviewID: "987",
			}}
			svc := New(nil, st)

			if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, tt.body, tt.githubReviewID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if st.updateCalls != 0 {
				t.Fatalf("mismatched retry should not rewrite: update=%d", st.updateCalls)
			}
		})
	}
}

// recordingSink captures what the review service reports.
type recordingSink struct{ events []ports.TelemetryEvent }

func (r *recordingSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	r.events = append(r.events, ev)
}
func (r *recordingSink) Close(context.Context) error { return nil }

func (r *recordingSink) named(name string) []ports.TelemetryEvent {
	var out []ports.TelemetryEvent
	for _, ev := range r.events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

// Code review shipped with no telemetry at all, so there was no way to tell
// whether reviewers approve or request changes. This pins the outcome event.
func TestSubmitReportsReviewOutcome(t *testing.T) {
	created := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "worker-1", Status: domain.ReviewRunRunning,
			Harness: "claude-code", CreatedAt: created,
			PRURL: "https://github.com/acme/secret-repo/pull/7", TargetSHA: "deadbeefcafe",
		},
	}
	sink := &recordingSink{}
	svc := New(nil, store,
		WithTelemetry(sink),
		WithClock(func() time.Time { return created.Add(90 * time.Second) }),
	)

	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "req-1")
	if _, err := svc.Submit(ctx, "worker-1", "run-1",
		domain.VerdictChangesRequested, "please rename this", "gh-review-42"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	got := sink.named("ao.review.submitted")
	if len(got) != 1 {
		t.Fatalf("ao.review.submitted count = %d, want 1", len(got))
	}
	p := got[0].Payload
	if p["verdict"] != string(domain.VerdictChangesRequested) {
		t.Fatalf("verdict = %#v, want changes_requested", p["verdict"])
	}
	if p["harness"] != "claude-code" {
		t.Fatalf("harness = %#v, want claude-code", p["harness"])
	}
	if p["duration_ms"] != int64(90_000) {
		t.Fatalf("duration_ms = %#v, want 90000", p["duration_ms"])
	}
	if p["posted_to_provider"] != true {
		t.Fatalf("posted_to_provider = %#v, want true", p["posted_to_provider"])
	}
	if got[0].SessionID == nil || *got[0].SessionID != "worker-1" {
		t.Fatalf("SessionID = %#v, want worker-1", got[0].SessionID)
	}
	// The emit path detaches from the request context on purpose; the request id
	// must still be carried so review rows join to the HTTP request.
	if got[0].RequestID != "req-1" {
		t.Fatalf("RequestID = %q, want req-1", got[0].RequestID)
	}
}

// The review body is reviewer prose about someone's source code, and the PR URL
// and SHA identify the repository. None may ever reach the payload, regardless of
// what the daemon's remote allowlist would strip later.
func TestSubmitNeverReportsReviewProseOrRepoIdentifiers(t *testing.T) {
	store := &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "worker-1", Status: domain.ReviewRunRunning,
			Harness: "codex", CreatedAt: time.Now().UTC(),
			PRURL: "https://github.com/acme/secret-repo/pull/7", TargetSHA: "deadbeefcafe",
		},
	}
	sink := &recordingSink{}
	svc := New(nil, store, WithTelemetry(sink))

	body := "leaks credentials in src/config/prod.ts"
	if _, err := svc.Submit(context.Background(), "worker-1", "run-1",
		domain.VerdictChangesRequested, body, ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	for _, ev := range sink.events {
		for key, value := range ev.Payload {
			text, ok := value.(string)
			if !ok {
				continue
			}
			for _, forbidden := range []string{body, "secret-repo", "deadbeefcafe", "prod.ts", "github.com"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("payload %q leaked %q: %q", key, forbidden, text)
				}
			}
		}
		if _, ok := ev.Payload["body"]; ok {
			t.Fatalf("payload carries a body key: %#v", ev.Payload)
		}
	}
	if p := sink.named("ao.review.submitted")[0].Payload; p["posted_to_provider"] != false {
		t.Fatalf("posted_to_provider = %#v, want false when nothing was posted", p["posted_to_provider"])
	}
}

// Re-submitting an already-complete run is idempotent in the store, so it must be
// idempotent in telemetry too, or a retrying reviewer would double-count verdicts.
func TestResubmitDoesNotDoubleReport(t *testing.T) {
	store := &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "worker-1", Status: domain.ReviewRunRunning,
			Harness: "opencode", CreatedAt: time.Now().UTC(),
		},
	}
	sink := &recordingSink{}
	svc := New(nil, store, WithTelemetry(sink))

	for i := 0; i < 3; i++ {
		if _, err := svc.Submit(context.Background(), "worker-1", "run-1",
			domain.VerdictApproved, "", ""); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	if got := len(sink.named("ao.review.submitted")); got != 1 {
		t.Fatalf("ao.review.submitted count = %d, want 1 across three submits", got)
	}
}

// Every existing caller constructs the service without a sink, so an unwired
// service must stay silent rather than panic.
func TestServiceWithoutTelemetrySinkStaysSilent(t *testing.T) {
	store := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "worker-1", Status: domain.ReviewRunRunning},
	}
	svc := New(nil, store)
	if _, err := svc.Submit(context.Background(), "worker-1", "run-1", domain.VerdictApproved, "", ""); err != nil {
		t.Fatalf("Submit without a sink: %v", err)
	}
}

// reviewErrorKind must distinguish the engine's sentinels. They are wrapped with
// %w and only become *apierr.Error at the HTTP boundary, so the generic
// classifier would report every trigger failure as "internal" and the
// trigger_failed event's error_kind could never say why.
func TestReviewErrorKindClassifiesEngineSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"invalid", fmt.Errorf("%w: no PR", reviewcore.ErrInvalid), "invalid"},
		{"not_found", fmt.Errorf("%w: worker gone", reviewcore.ErrNotFound), "not_found"},
		{"agent_unavailable", fmt.Errorf("%w", ports.ErrAgentBinaryNotFound), "agent_unavailable"},
		{"fallback_internal", errors.New("something unexpected"), "internal"},
	}
	for _, c := range cases {
		if got := reviewErrorKind(c.err); got != c.want {
			t.Errorf("%s: reviewErrorKind = %q, want %q", c.name, got, c.want)
		}
	}
}

// An automatic pass was previously invisible: only the manual Trigger emitted,
// so auto-review could not be told apart from manual review anywhere
// downstream even though the two answer different product questions.
func TestTriggerReportsWhoStartedThePass(t *testing.T) {
	cases := []struct {
		name string
		call func(*Service) error
		want string
	}{
		{"manual", func(s *Service) error {
			_, err := s.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Config: domain.AgentConfig{}})
			return err
		}, "manual"},
		{"auto", func(s *Service) error {
			_, err := s.TriggerAuto(context.Background(), "worker-1", "claude-code")
			return err
		}, "auto"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			svc := New(nil, &fakeStore{}, WithTelemetry(sink))
			svc.engineTrigger = func(
				_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions,
			) (reviewcore.TriggerResult, error) {
				return reviewcore.TriggerResult{
					Run:         domain.ReviewRun{Harness: "claude-code"},
					CreatedRuns: []domain.ReviewRun{{ID: "run-1"}},
				}, nil
			}
			if err := c.call(svc); err != nil {
				t.Fatalf("trigger: %v", err)
			}
			got := sink.named("ao.review.triggered")
			if len(got) != 1 {
				t.Fatalf("ao.review.triggered count = %d, want 1", len(got))
			}
			if got[0].Payload["trigger"] != c.want {
				t.Fatalf("trigger = %#v, want %q", got[0].Payload["trigger"], c.want)
			}
			if got[0].Payload["reused"] != false {
				t.Fatalf("reused = %#v, want false", got[0].Payload["reused"])
			}
		})
	}
}

func TestTriggerFailureReportsWhichPassFailed(t *testing.T) {
	sink := &recordingSink{}
	svc := New(nil, &fakeStore{}, WithTelemetry(sink))
	svc.engineTrigger = func(
		_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions,
	) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{}, fmt.Errorf("%w: no PR", reviewcore.ErrInvalid)
	}

	if _, err := svc.TriggerAuto(context.Background(), "worker-1", "codex"); err == nil {
		t.Fatal("TriggerAuto: want error")
	}
	if got := sink.named("ao.review.triggered"); len(got) != 1 {
		t.Fatalf("ao.review.triggered count = %d, want 1 even on failure", len(got))
	} else if got[0].Payload["trigger"] != "auto" {
		t.Fatalf("triggered payload = %#v, want trigger=auto", got[0].Payload)
	}
	got := sink.named("ao.review.trigger_failed")
	if len(got) != 1 {
		t.Fatalf("ao.review.trigger_failed count = %d, want 1", len(got))
	}
	if got[0].Payload["error_kind"] != "invalid" || got[0].Payload["trigger"] != "auto" {
		t.Fatalf("payload = %#v, want error_kind=invalid trigger=auto", got[0].Payload)
	}
}

func TestTriggerRejectsInvalidReviewerConfigBeforeEngine(t *testing.T) {
	sink := &recordingSink{}
	svc := New(nil, &fakeStore{}, WithTelemetry(sink))
	called := false
	svc.engineTrigger = func(
		_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions,
	) (reviewcore.TriggerResult, error) {
		called = true
		return reviewcore.TriggerResult{}, nil
	}

	if _, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Config: domain.AgentConfig{Mode: "turbo"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("engineTrigger should not run for invalid config")
	}
	got := sink.named("ao.review.trigger_failed")
	if len(got) != 1 {
		t.Fatalf("ao.review.trigger_failed count = %d, want 1", len(got))
	}
	if got[0].Payload["error_kind"] != "invalid" || got[0].Payload["trigger"] != "manual" {
		t.Fatalf("payload = %#v, want error_kind=invalid trigger=manual", got[0].Payload)
	}
}

// The submitted event has to carry enough to tell a shallow automatic approval
// apart from a substantial manual changes-requested pass.
func TestSubmitReportsPassShapeNotItsContents(t *testing.T) {
	policyOff := false
	store := &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "worker-1", Status: domain.ReviewRunRunning,
			Harness: "codex", TriggerSource: domain.ReviewTriggerAuto, CreatedAt: time.Now().UTC(),
		},
		sessionAutoInjectReview: &policyOff,
	}
	sink := &recordingSink{}
	svc := New(nil, store, WithTelemetry(sink))

	body := "rename this symbol"
	if _, err := svc.Submit(context.Background(), "worker-1", "run-1",
		domain.VerdictChangesRequested, body, ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	p := sink.named("ao.review.submitted")[0].Payload
	if p["trigger"] != string(domain.ReviewTriggerAuto) {
		t.Fatalf("trigger = %#v, want auto", p["trigger"])
	}
	if p["body_bytes"] != len(body) {
		t.Fatalf("body_bytes = %#v, want %d", p["body_bytes"], len(body))
	}
	if p["auto_inject"] != false {
		t.Fatalf("auto_inject = %#v, want false for a session with the policy off", p["auto_inject"])
	}
}
func TestRestartedManualPassIsNotReportedAsReused(t *testing.T) {
	sink := &recordingSink{}
	svc := New(nil, &fakeStore{}, WithTelemetry(sink))
	svc.engineTrigger = func(
		_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions,
	) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Run: domain.ReviewRun{Harness: "codex"}, Created: true, CreatedRuns: nil}, nil
	}

	if _, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Config: domain.AgentConfig{Model: "gpt-5-mini"}}); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	got := sink.named("ao.review.triggered")
	if len(got) != 1 {
		t.Fatalf("ao.review.triggered count = %d, want 1", len(got))
	}
	if got[0].Payload["reused"] != false || got[0].Payload["created_runs"] != 0 {
		t.Fatalf("payload = %#v, want reused=false created_runs=0 for a restart", got[0].Payload)
	}
}

func TestReusedManualPassStaysATrigger(t *testing.T) {
	sink := &recordingSink{}
	svc := New(nil, &fakeStore{}, WithTelemetry(sink))
	svc.engineTrigger = func(
		_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions,
	) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Run: domain.ReviewRun{Harness: "codex"}, CreatedRuns: nil}, nil
	}

	if _, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Config: domain.AgentConfig{}}); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	got := sink.named("ao.review.triggered")
	if len(got) != 1 {
		t.Fatalf("ao.review.triggered count = %d, want 1", len(got))
	}
	if got[0].Payload["reused"] != true || got[0].Payload["trigger"] != "manual" {
		t.Fatalf("payload = %#v, want reused=true trigger=manual", got[0].Payload)
	}
	if n := len(sink.events); n != 1 {
		t.Fatalf("emitted %d events, want only the trigger for a manual reuse", n)
	}
}

// ao.review.triggered now counts every attempt, including automatic no-op
// sweeps. created_runs and reused distinguish real work from an already
// running or skipped pass.
func TestReusedOrSkippedAutoPassStillCountsAsTriggered(t *testing.T) {
	cases := []struct {
		name    string
		result  reviewcore.TriggerResult
		harness any
	}{
		{"reused: a reviewer is already running", reviewcore.TriggerResult{
			Run: domain.ReviewRun{Harness: "claude-code"}, CreatedRuns: nil,
		}, "claude-code"},
		{"skipped: the session changed under the coordinator", reviewcore.TriggerResult{
			SkipReason: "worker_active",
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			svc := New(nil, &fakeStore{}, WithTelemetry(sink))
			svc.engineTrigger = func(
				_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions,
			) (reviewcore.TriggerResult, error) {
				return c.result, nil
			}

			for i := 0; i < 6; i++ {
				if _, err := svc.TriggerAuto(context.Background(), "worker-1", "claude-code"); err != nil {
					t.Fatalf("TriggerAuto %d: %v", i, err)
				}
			}
			got := sink.named("ao.review.triggered")
			if len(got) != 6 {
				t.Fatalf("ao.review.triggered count = %d, want 6", len(got))
			}
			for i, ev := range got {
				if ev.Payload["trigger"] != "auto" || ev.Payload["created_runs"] != 0 || ev.Payload["reused"] != true {
					t.Fatalf("event %d payload = %#v, want trigger=auto created_runs=0 reused=true", i, ev.Payload)
				}
				if ev.Payload["harness"] != c.harness {
					t.Fatalf("event %d harness = %#v, want %#v", i, ev.Payload["harness"], c.harness)
				}
			}
			if failed := sink.named("ao.review.trigger_failed"); len(failed) != 0 {
				t.Fatalf("ao.review.trigger_failed count = %d, want 0", len(failed))
			}
		})
	}
}

func TestTriggerRequestedEnablesAutoInjectOnlyAfterAPassStarts(t *testing.T) {
	off := false
	st := &fakeStore{sessionAutoInjectReview: &off}
	sink := &recordingSink{}
	svc := New(nil, st, WithTelemetry(sink))
	var gotOpts reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, opts reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		gotOpts = opts
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "run-1"}}}, nil
	}

	out, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{
		Harness: domain.ReviewerCodex, Source: domain.ReviewTriggerAgent, RejectReviewedHead: true, EnableAutoInject: true,
	})
	if err != nil {
		t.Fatalf("TriggerRequested: %v", err)
	}
	if !out.AutoInjectEnabled || len(st.autoInjectSets) != 1 || !st.autoInjectSets[0] {
		t.Fatalf("outcome=%+v sets=%v, want auto-inject turned on once", out, st.autoInjectSets)
	}
	if gotOpts.Source != domain.ReviewTriggerAgent || !gotOpts.RejectReviewedHead || gotOpts.Harness != domain.ReviewerCodex {
		t.Fatalf("engine opts = %+v, want the request forwarded", gotOpts)
	}
	if got := sink.named("ao.review.triggered"); len(got) != 1 || got[0].Payload["trigger"] != "agent" {
		t.Fatalf("triggered events = %+v, want one agent trigger", got)
	}

	// Already on: nothing to change and nothing to report.
	out, err = svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Source: domain.ReviewTriggerAgent, EnableAutoInject: true})
	if err != nil {
		t.Fatalf("second TriggerRequested: %v", err)
	}
	if out.AutoInjectEnabled || len(st.autoInjectSets) != 1 {
		t.Fatalf("outcome=%+v sets=%v, want no second write", out, st.autoInjectSets)
	}
}

func TestTriggerRequestedLeavesAutoInjectWhenTheTriggerIsRejected(t *testing.T) {
	off := false
	st := &fakeStore{sessionAutoInjectReview: &off}
	svc := New(nil, st)
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{}, fmt.Errorf("%w: PR #1 head abc was already reviewed", reviewcore.ErrHeadAlreadyReviewed)
	}

	_, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true, EnableAutoInject: true})
	if !errors.Is(err, ErrHeadAlreadyReviewed) || !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrHeadAlreadyReviewed", err)
	}
	if len(st.autoInjectSets) != 0 {
		t.Fatalf("a rejected trigger changed auto-inject: %v", st.autoInjectSets)
	}
	if kind := reviewErrorKind(err); kind != "conflict" {
		t.Fatalf("error kind = %q, want conflict", kind)
	}
}

func TestTriggerRequestedRejectsDaemonOnlySourceAndContradictoryPolicy(t *testing.T) {
	svc := New(nil, &fakeStore{})
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		t.Fatal("engine must not run")
		return reviewcore.TriggerResult{}, nil
	}
	for _, req := range []TriggerRequest{
		{Source: domain.ReviewTriggerAuto},
		{Source: "robot"},
		{Rerun: true, RejectReviewedHead: true},
	} {
		if _, err := svc.TriggerRequested(context.Background(), "worker-1", req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("req %+v: err = %v, want ErrInvalid", req, err)
		}
	}
}

type fakePRRefresher struct {
	calls []string
	err   map[string]error
	// order records refreshes relative to the engine trigger.
	order *[]string
}

func (f *fakePRRefresher) RefreshPR(_ context.Context, _ domain.SessionID, prURL string) error {
	f.calls = append(f.calls, prURL)
	if f.order != nil {
		*f.order = append(*f.order, "refresh "+prURL)
	}
	return f.err[prURL]
}

// A requested review fetches the worker's open PRs fresh first, so it reviews
// the commit really on the PR, and only then plans the pass.
func TestTriggerRequestedRefreshesOpenPRsBeforePlanning(t *testing.T) {
	var order []string
	st := &fakeStore{prs: []domain.PullRequest{
		{URL: "https://api/pr/1", HTMLURL: "https://github.com/o/r/pull/1"},
		{URL: "https://api/pr/2", HTMLURL: "https://github.com/o/r/pull/2", Merged: true},
	}}
	refresher := &fakePRRefresher{order: &order, err: map[string]error{"https://github.com/o/r/pull/1": errors.New("provider down")}}
	svc := New(nil, st, WithPRRefresher(refresher))
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		order = append(order, "trigger")
		return reviewcore.TriggerResult{Created: true}, nil
	}

	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true}); err != nil {
		t.Fatalf("a failed best-effort refresh must fall back to stored facts: %v", err)
	}
	if strings.Join(order, ",") != "refresh https://github.com/o/r/pull/1,trigger" {
		t.Fatalf("order = %v, want the open PR refreshed (merged one skipped) before planning", order)
	}

	order = nil
	if _, err := svc.TriggerAuto(context.Background(), "mer-1", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "trigger" {
		t.Fatalf("auto-review must not refresh (the observer just ran): %v", order)
	}
}

// Naming a PR AO does not track yet attaches it and reviews only it. If it
// cannot be fetched there is nothing to review, so that is an error.
func TestTriggerRequestedNamedPRAttachesItAndTargetsIt(t *testing.T) {
	st := &fakeStore{}
	refresher := &fakePRRefresher{}
	svc := New(nil, st, WithPRRefresher(refresher))
	var got reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, opts reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		got = opts
		return reviewcore.TriggerResult{Created: true}, nil
	}
	url := "https://github.com/o/r/pull/9"
	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{Source: domain.ReviewTriggerAgent, PRURL: " " + url + " "}); err != nil {
		t.Fatalf("TriggerRequested: %v", err)
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != url || got.PRURL != url {
		t.Fatalf("refresh=%v target=%q, want the named PR attached then targeted", refresher.calls, got.PRURL)
	}

	refresher.err = map[string]error{url: fmt.Errorf("%w: owned", reviewcore.ErrPROwnedElsewhere)}
	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{PRURL: url}); !errors.Is(err, ErrPROwnedElsewhere) {
		t.Fatalf("err = %v, want the attach failure for an untracked PR", err)
	}
}
