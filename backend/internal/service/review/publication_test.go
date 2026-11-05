package review

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Target URLs here exercise receipt binding only; these tests never contact SCM.
func TestSubmitPublicationStateControlsRefreshAndTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name        string
		publication *domain.ReviewPublication
		posted      bool
	}{
		{name: "published", publication: &domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "101"}}}, posted: true},
		{name: "blocked", publication: &domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "permission denied"}},
		{name: "unconfirmed", publication: &domain.ReviewPublication{Provider: "gitlab", Status: "unconfirmed", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "timeout after POST", Refs: []domain.ReviewPublicationRef{{Kind: "draft_note", ID: "42"}}}},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Status: domain.ReviewRunRunning}}
			refresh := &recordingReviewRefresher{}
			sink := &recordingSink{}
			svc := New(nil, st, WithThreadRefresher(refresh), WithTelemetry(sink))
			input := SubmittedReview{RunID: "run", Verdict: domain.VerdictApproved, Body: "Local review complete", Publication: tc.publication}

			runs, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 {
				t.Fatalf("runs = %+v, want one result", runs)
			}
			if !reflect.DeepEqual(tc.publication, runs[0].Publication) {
				t.Errorf("publication = %+v, want %+v", runs[0].Publication, tc.publication)
			}
			if runs[0].GithubReviewID != "" {
				t.Errorf("GitLab review ID = %q, want empty", runs[0].GithubReviewID)
			}
			if got := runs[0].PublishedToProvider(); got != tc.posted {
				t.Errorf("published = %v, want %v", got, tc.posted)
			}
			if got := len(refresh.prURLs) == 1; got != tc.posted {
				t.Errorf("refresh targets = %v, posted=%v", refresh.prURLs, tc.posted)
			}
			events := sink.named("ao.review.submitted")
			if len(events) != 1 {
				t.Fatalf("submit events = %+v, want one", events)
			}
			if got := events[0].Payload["posted_to_provider"]; got != tc.posted {
				t.Errorf("posted_to_provider = %v, want %v", got, tc.posted)
			}

			// A changed session toggle and an idempotent AO retry cannot rewrite
			// the original snapshot or schedule another provider refresh.
			disabled := false
			st.sessionAutoInjectReview = &disabled
			_, err = svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

			if err != nil {
				t.Fatal(err)
			}
			if !st.run.AutoInjectReview {
				t.Error("retry changed the original auto-inject snapshot")
			}
			if st.updateCalls != 1 {
				t.Errorf("result updates = %d, want one", st.updateCalls)
			}
			if got := len(sink.named("ao.review.submitted")); got != 1 {
				t.Errorf("submit events after retry = %d, want one", got)
			}
			if got := len(refresh.prURLs) == 1; got != tc.posted {
				t.Errorf("refresh targets = %v, posted=%v", refresh.prURLs, tc.posted)
			}
		})
	}
}

func TestSubmitRejectsInvalidPublicationBindingAndEvidence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		publication domain.ReviewPublication
	}{
		{name: "wrong host", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://other.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "denied"}},
		{name: "wrong subgroup", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/other/r/-/merge_requests/39", TargetSHA: "head", Error: "denied"}},
		{name: "wrong iid", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/40", TargetSHA: "head", Error: "denied"}},
		{name: "wrong provider", publication: domain.ReviewPublication{Provider: "github", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "denied"}},
		{name: "changed head", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "new-head", Error: "head changed"}},
		{name: "missing summary", publication: domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head"}},
		{name: "missing required discussion", publication: domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "101"}}}},
		{name: "fake review id", publication: domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "101"}}}},
		{name: "uncertain without reason", publication: domain.ReviewPublication{Provider: "gitlab", Status: "unconfirmed", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head"}},
		{name: "missing head", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", Error: "denied"}},
		{name: "unknown status", publication: domain.ReviewPublication{Provider: "gitlab", Status: "unknown", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head"}},
		{name: "published with error", publication: domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "denied"}},
		{name: "duplicate ref", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "denied", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "1"}, {Kind: "note", ID: "1"}}}},
		{name: "empty ref id", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "denied", Refs: []domain.ReviewPublicationRef{{Kind: "note"}}}},
		{name: "control in ref id", publication: domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Error: "denied", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "1\n2"}}}},
		{name: "pending published draft", publication: domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "draft_note", ID: "1"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: "https://gitlab.example/g/sub/r/-/merge_requests/39", TargetSHA: "head", Status: domain.ReviewRunRunning}}
			refresh := &recordingReviewRefresher{}
			svc := New(nil, st, WithThreadRefresher(refresh))

			_, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{{RunID: "run", Verdict: domain.VerdictChangesRequested, Body: "Fix main.go", Publication: &tc.publication}})

			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("submit error = %v, want ErrInvalid", err)
			}
			if st.updateCalls != 0 {
				t.Errorf("result updates = %d, want zero", st.updateCalls)
			}
			if len(refresh.prURLs) != 0 {
				t.Errorf("invalid receipt scheduled refreshes: %v", refresh.prURLs)
			}
		})
	}
}

func TestSubmitPublicationReplayRejectsReplacedReceipt(t *testing.T) {
	st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: "https://gitlab.example/g/r/-/merge_requests/1", TargetSHA: "head", Status: domain.ReviewRunRunning}}
	svc := New(nil, st)
	p := &domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: st.run.PRURL, TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "1"}, {Kind: "discussion", ID: "abc"}, {Kind: "note", ID: "2"}}}
	input := SubmittedReview{RunID: "run", Verdict: domain.VerdictChangesRequested, Body: "Fix main.go", Publication: p}
	_, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})
	if err != nil {
		t.Fatal(err)
	}

	p.Refs[0].ID = "different"
	_, err = svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("submit error = %v, want ErrInvalid", err)
	}
	if st.updateCalls != 1 {
		t.Errorf("result updates = %d, want one", st.updateCalls)
	}
	if st.run.Publication == nil || len(st.run.Publication.Refs) == 0 || st.run.Publication.Refs[0].ID != "abc" {
		t.Fatalf("stored receipt changed with caller mutation: %+v", st.run.Publication)
	}

	// Legacy clients can replay without a receipt, but cannot erase it.
	input.Publication = nil
	_, err = svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})
	if err != nil {
		t.Fatal(err)
	}
	if st.run.Publication == nil {
		t.Error("omitted replay erased the stored receipt")
	}
}

func TestSubmitMixedProviderReceiptsPreserveLegacyGitHubID(t *testing.T) {
	st := &fakeStore{batchRuns: []domain.ReviewRun{
		{ID: "old", SessionID: "worker", Status: domain.ReviewRunCancelled},
		{ID: "gh", SessionID: "worker", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "gh-head", Status: domain.ReviewRunRunning},
		{ID: "gl", SessionID: "worker", PRURL: "https://gitlab.example/g/r/-/merge_requests/1", TargetSHA: "gl-head", Status: domain.ReviewRunRunning},
	}}
	refresh := &recordingReviewRefresher{}
	svc := New(nil, st, WithThreadRefresher(refresh))

	runs, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{
		{RunID: "old", Verdict: domain.VerdictApproved, Publication: &domain.ReviewPublication{Status: "invalid"}},
		{RunID: "gh", Verdict: domain.VerdictApproved, Publication: &domain.ReviewPublication{Provider: "github", Status: "published", PRURL: st.batchRuns[1].PRURL, TargetSHA: "gh-head", Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "123"}}}},
		{RunID: "gl", Verdict: domain.VerdictApproved, Publication: &domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: st.batchRuns[2].PRURL, TargetSHA: "gl-head", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "123"}}}},
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want two current results", runs)
	}
	if runs[0].GithubReviewID != "123" {
		t.Errorf("GitHub review ID = %q, want 123", runs[0].GithubReviewID)
	}
	if runs[1].GithubReviewID != "" {
		t.Errorf("GitLab review ID = %q, want empty", runs[1].GithubReviewID)
	}
	if want := []string{runs[0].PRURL, runs[1].PRURL}; !reflect.DeepEqual(want, refresh.prURLs) {
		t.Errorf("refresh targets = %v, want %v", refresh.prURLs, want)
	}
}

func TestSubmitDeliveredPublicationDoesNotRewriteResult(t *testing.T) {
	publication := &domain.ReviewPublication{Provider: "github", Status: "published", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "123"}}}
	stored := domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: publication.PRURL, TargetSHA: "head", Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved, Body: "approved", GithubReviewID: "123", Publication: publication}
	st := &fakeStore{ok: true, run: stored}
	svc := New(nil, st)

	result, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{{RunID: "run", Verdict: domain.VerdictChangesRequested, Body: "late result", Publication: &domain.ReviewPublication{Status: "invalid"}}})

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]domain.ReviewRun{stored}, result) {
		t.Errorf("delivered result = %+v, want %+v", result, stored)
	}
	if st.updateCalls != 0 {
		t.Errorf("result updates = %d, want zero", st.updateCalls)
	}
}

func TestSubmitRejectsGitLabReceiptWithGitHubReviewID(t *testing.T) {
	st := &fakeStore{ok: true, run: domain.ReviewRun{
		ID: "run", SessionID: "worker", PRURL: "https://gitlab.example/g/r/-/merge_requests/1",
		TargetSHA: "head", Status: domain.ReviewRunRunning,
	}}
	original := st.run
	refresh := &recordingReviewRefresher{}
	svc := New(nil, st, WithThreadRefresher(refresh))
	input := SubmittedReview{
		RunID: "run", Verdict: domain.VerdictApproved, GithubReviewID: "123",
		Publication: &domain.ReviewPublication{
			Provider: "gitlab", Status: "published", PRURL: st.run.PRURL, TargetSHA: "head",
			Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "101"}},
		},
	}

	for _, name := range []string{"first submission", "identical repeat"} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("submit error = %v, want ErrInvalid", err)
			}
			if st.updateCalls != 0 {
				t.Errorf("result updates = %d, want zero", st.updateCalls)
			}
			if !reflect.DeepEqual(original, st.run) {
				t.Errorf("invalid submission changed run: got %+v, want %+v", st.run, original)
			}
			if len(refresh.prURLs) != 0 {
				t.Errorf("invalid submission scheduled refreshes: %v", refresh.prURLs)
			}
		})
	}
}

func TestSubmitGitLabReceiptWithEmptyGitHubIDIsIdempotent(t *testing.T) {
	st := &fakeStore{ok: true, run: domain.ReviewRun{
		ID: "run", SessionID: "worker", PRURL: "https://gitlab.example/g/r/-/merge_requests/1",
		TargetSHA: "head", Status: domain.ReviewRunRunning,
	}}
	refresh := &recordingReviewRefresher{}
	svc := New(nil, st, WithThreadRefresher(refresh))
	publication := &domain.ReviewPublication{
		Provider: "gitlab", Status: "published", PRURL: st.run.PRURL, TargetSHA: "head",
		Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "101"}},
	}
	input := SubmittedReview{RunID: "run", Verdict: domain.VerdictApproved, Publication: publication}

	first, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

	if err != nil || len(first) != 1 {
		t.Fatalf("first result = %+v, err=%v", first, err)
	}
	if first[0].Status != domain.ReviewRunComplete || first[0].GithubReviewID != "" || !reflect.DeepEqual(publication, first[0].Publication) {
		t.Fatalf("recorded result = %+v, want complete GitLab receipt without a GitHub review ID", first[0])
	}

	repeated, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

	if err != nil {
		t.Fatalf("repeat result: %v", err)
	}
	if !reflect.DeepEqual(first, repeated) {
		t.Errorf("repeated result = %+v, want %+v", repeated, first)
	}
	if st.updateCalls != 1 {
		t.Errorf("result updates = %d, want one", st.updateCalls)
	}
	if want := []string{st.run.PRURL}; !reflect.DeepEqual(want, refresh.prURLs) {
		t.Errorf("refresh targets = %v, want %v", refresh.prURLs, want)
	}
}

func TestSubmitRejectsContradictoryGitHubReceipt(t *testing.T) {
	for _, tc := range []struct{ name, status, id, reason string }{
		{name: "different id", status: "published", id: "999"},
		{name: "unconfirmed with confirmed id", status: "unconfirmed", id: "123", reason: "timeout"},
		{name: "blocked with confirmed id", status: "blocked", id: "123", reason: "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "head", Status: domain.ReviewRunRunning}}
			svc := New(nil, st)

			_, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{{RunID: "run", Verdict: domain.VerdictApproved, GithubReviewID: tc.id, Publication: &domain.ReviewPublication{Provider: "github", Status: tc.status, PRURL: st.run.PRURL, TargetSHA: "head", Error: tc.reason, Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "123"}}}}})

			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("submit error = %v, want ErrInvalid", err)
			}
			if st.updateCalls != 0 {
				t.Errorf("result updates = %d, want zero", st.updateCalls)
			}
		})
	}
}
