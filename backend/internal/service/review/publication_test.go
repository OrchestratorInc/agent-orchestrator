package review

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

			require.NoError(t, err)
			require.Len(t, runs, 1)
			assert.Equal(t, tc.publication, runs[0].Publication)
			assert.Empty(t, runs[0].GithubReviewID)
			assert.Equal(t, tc.posted, runs[0].PublishedToProvider())
			assert.Equal(t, tc.posted, len(refresh.prURLs) == 1)
			events := sink.named("ao.review.submitted")
			require.Len(t, events, 1)
			assert.Equal(t, tc.posted, events[0].Payload["posted_to_provider"])

			// A changed session toggle and an idempotent AO retry cannot rewrite
			// the original snapshot or schedule another provider refresh.
			disabled := false
			st.sessionAutoInjectReview = &disabled
			_, err = svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

			require.NoError(t, err)
			assert.True(t, st.run.AutoInjectReview)
			assert.Equal(t, 1, st.updateCalls)
			assert.Len(t, sink.named("ao.review.submitted"), 1)
			assert.Equal(t, tc.posted, len(refresh.prURLs) == 1)
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

			require.ErrorIs(t, err, ErrInvalid)
			assert.Zero(t, st.updateCalls)
			assert.Empty(t, refresh.prURLs)
		})
	}
}

func TestSubmitPublicationReplayRejectsReplacedReceipt(t *testing.T) {
	st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: "https://gitlab.example/g/r/-/merge_requests/1", TargetSHA: "head", Status: domain.ReviewRunRunning}}
	svc := New(nil, st)
	p := &domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: st.run.PRURL, TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "note", ID: "1"}, {Kind: "discussion", ID: "abc"}, {Kind: "note", ID: "2"}}}
	input := SubmittedReview{RunID: "run", Verdict: domain.VerdictChangesRequested, Body: "Fix main.go", Publication: p}
	_, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})
	require.NoError(t, err)

	p.Refs[0].ID = "different"
	_, err = svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})

	require.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, 1, st.updateCalls)
	assert.Equal(t, "abc", st.run.Publication.Refs[0].ID)

	// Legacy clients can replay without a receipt, but cannot erase it.
	input.Publication = nil
	_, err = svc.SubmitMany(t.Context(), "worker", []SubmittedReview{input})
	require.NoError(t, err)
	assert.NotNil(t, st.run.Publication)
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

	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, "123", runs[0].GithubReviewID)
	assert.Empty(t, runs[1].GithubReviewID)
	assert.Equal(t, []string{runs[0].PRURL, runs[1].PRURL}, refresh.prURLs)
}

func TestSubmitDeliveredPublicationDoesNotRewriteResult(t *testing.T) {
	publication := &domain.ReviewPublication{Provider: "github", Status: "published", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "123"}}}
	stored := domain.ReviewRun{ID: "run", SessionID: "worker", PRURL: publication.PRURL, TargetSHA: "head", Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved, Body: "approved", GithubReviewID: "123", Publication: publication}
	st := &fakeStore{ok: true, run: stored}
	svc := New(nil, st)

	result, err := svc.SubmitMany(t.Context(), "worker", []SubmittedReview{{RunID: "run", Verdict: domain.VerdictChangesRequested, Body: "late result", Publication: &domain.ReviewPublication{Status: "invalid"}}})

	require.NoError(t, err)
	assert.Equal(t, []domain.ReviewRun{stored}, result)
	assert.Zero(t, st.updateCalls)
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

			require.ErrorIs(t, err, ErrInvalid)
			assert.Zero(t, st.updateCalls)
		})
	}
}
