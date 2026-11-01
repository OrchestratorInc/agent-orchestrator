package pr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeStateCommentSCM struct {
	stateErr        error
	stateRequests   []ports.SCMPRStateRequest
	commentErr      error
	commentResult   ports.SCMCommentResult
	commentRequests []ports.SCMCommentRequest
}

func (f *fakeStateCommentSCM) ChangePullRequestState(_ context.Context, request ports.SCMPRStateRequest) error {
	f.stateRequests = append(f.stateRequests, request)
	return f.stateErr
}

func (f *fakeStateCommentSCM) PostComment(_ context.Context, request ports.SCMCommentRequest) (ports.SCMCommentResult, error) {
	f.commentRequests = append(f.commentRequests, request)
	return f.commentResult, f.commentErr
}

type fakeStateWriter struct {
	url       string
	state     domain.PRState
	changedAt time.Time
	calls     int
	err       error
}

func (f *fakeStateWriter) MarkPRState(_ context.Context, prURL string, state domain.PRState, changedAt time.Time) error {
	f.calls++
	f.url, f.state, f.changedAt = prURL, state, changedAt
	return f.err
}

type fakeCommentWriter struct {
	reviewComments []domain.PullRequestComment
	conversation   []domain.PullRequestConversationComment
}

func (f *fakeCommentWriter) UpsertPRReviewComment(_ context.Context, _ string, comment domain.PullRequestComment) error {
	f.reviewComments = append(f.reviewComments, comment)
	return nil
}

func (f *fakeCommentWriter) AppendPRConversationComment(_ context.Context, _ string, comment domain.PullRequestConversationComment) (bool, error) {
	f.conversation = append(f.conversation, comment)
	return true, nil
}

type fakeRefresher struct{ urls []string }

func (f *fakeRefresher) RequestReviewRefresh(prURL string) { f.urls = append(f.urls, prURL) }

func TestActionServiceMerge_UsesRequestedMethodAndDefaultsToSquash(t *testing.T) {
	for _, tc := range []struct {
		requested string
		want      ports.SCMMergeMethod
	}{{"", ports.SCMMergeSquash}, {"merge", ports.SCMMergeMerge}, {"REBASE", ports.SCMMergeRebase}} {
		pr, scm := mergeableActionFixture()
		svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm})
		result, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, Method: tc.requested})
		if err != nil {
			t.Fatalf("%q: %v", tc.requested, err)
		}
		if scm.request.Method != tc.want || result.Method != string(tc.want) || scm.request.ExpectedHeadSHA != pr.HeadSHA {
			t.Fatalf("%q: request=%#v result=%#v", tc.requested, scm.request, result)
		}
	}
}

func TestActionServiceMerge_RejectsUnknownMethodAndMapsUnsupported(t *testing.T) {
	pr, scm := mergeableActionFixture()
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm})
	if _, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, Method: "octopus"}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("error = %v, want invalid action", err)
	}
	if scm.mergeCalls != 0 {
		t.Fatalf("merge calls = %d, want 0", scm.mergeCalls)
	}
	scm.mergeErr = ports.ErrSCMUnsupported
	if _, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, Method: "rebase"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
}

func openPRFixture() domain.PullRequest {
	return domain.PullRequest{
		URL: "https://github.com/acme/widgets/pull/42", Number: 42, SessionID: "session-1",
		Provider: "github", Host: "github.com", Repo: "acme/widgets", ProviderID: "PR_node",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func TestActionServiceChangeState_TransitionsPersistAndNotifyLifecycle(t *testing.T) {
	changedAt := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		action string
		modify func(*domain.PullRequest)
		want   domain.PRState
	}{
		{action: "close", want: domain.PRStateClosed},
		{action: "close", modify: func(pr *domain.PullRequest) { pr.Draft = true }, want: domain.PRStateClosed},
		{action: "reopen", modify: func(pr *domain.PullRequest) { pr.Closed = true }, want: domain.PRStateOpen},
		{action: "ready", modify: func(pr *domain.PullRequest) { pr.Draft = true }, want: domain.PRStateOpen},
		{action: "draft", want: domain.PRStateDraft},
	} {
		t.Run(tc.action+"->"+string(tc.want), func(t *testing.T) {
			pr := openPRFixture()
			if tc.modify != nil {
				tc.modify(&pr)
			}
			scm := &fakeStateCommentSCM{}
			reader := &fakeSCMAction{observation: ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{URL: pr.URL, Number: 42}}}
			writer := &fakeStateWriter{}
			lifecycle := &fakeActionLifecycle{}
			svc := NewActionService(ActionDeps{
				Store: &fakeActionStore{pr: pr, ok: true}, Reader: reader, StateChanger: scm,
				StateWriter: writer, Lifecycle: lifecycle, Clock: func() time.Time { return changedAt },
			})
			result, err := svc.ChangeState(context.Background(), StateRequest{PRID: "42", PRURL: pr.URL, Action: tc.action})
			if err != nil {
				t.Fatal(err)
			}
			if result.PRNumber != 42 || result.State != tc.want || !result.StateChangedAt.Equal(changedAt) {
				t.Fatalf("result = %#v", result)
			}
			if len(scm.stateRequests) != 1 || string(scm.stateRequests[0].Action) != tc.action || scm.stateRequests[0].NodeID != "PR_node" || scm.stateRequests[0].PR.Number != 42 {
				t.Fatalf("scm requests = %#v", scm.stateRequests)
			}
			if writer.calls != 1 || writer.url != pr.URL || writer.state != tc.want || !writer.changedAt.Equal(changedAt) {
				t.Fatalf("writer = %#v", writer)
			}
			obs := lifecycle.obs.PR
			if lifecycle.calls != 1 || lifecycle.sessionID != pr.SessionID || obs.State != string(tc.want) ||
				obs.Closed != (tc.want == domain.PRStateClosed) || obs.Draft != (tc.want == domain.PRStateDraft) || !lifecycle.obs.ObservedAt.Equal(changedAt) {
				t.Fatalf("lifecycle calls=%d obs=%+v", lifecycle.calls, lifecycle.obs)
			}
		})
	}
}

func TestActionServiceChangeState_RejectsInvalidTransitionsWithoutCallingProvider(t *testing.T) {
	for _, tc := range []struct {
		action string
		modify func(*domain.PullRequest)
	}{
		{action: "reopen"},
		{action: "ready"},
		{action: "draft", modify: func(pr *domain.PullRequest) { pr.Draft = true }},
		{action: "close", modify: func(pr *domain.PullRequest) { pr.Closed = true }},
		{action: "draft", modify: func(pr *domain.PullRequest) { pr.Closed = true }},
		{action: "reopen", modify: func(pr *domain.PullRequest) { pr.Merged = true }},
	} {
		pr := openPRFixture()
		if tc.modify != nil {
			tc.modify(&pr)
		}
		scm := &fakeStateCommentSCM{}
		svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, StateChanger: scm})
		_, err := svc.ChangeState(context.Background(), StateRequest{PRID: "42", PRURL: pr.URL, Action: tc.action})
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s on %+v: error = %v, want invalid transition", tc.action, pr, err)
		}
		if len(scm.stateRequests) != 0 {
			t.Fatalf("%s: provider called for an invalid transition", tc.action)
		}
	}
}

func TestActionServiceChangeState_ErrorMapping(t *testing.T) {
	pr := openPRFixture()
	for _, tc := range []struct {
		name    string
		store   *fakeActionStore
		request StateRequest
		scmErr  error
		want    error
	}{
		{name: "unknown pr", store: &fakeActionStore{}, request: StateRequest{PRID: "42", PRURL: pr.URL, Action: "close"}, want: ErrPRNotFound},
		{name: "number mismatch", store: &fakeActionStore{pr: pr, ok: true}, request: StateRequest{PRID: "7", PRURL: pr.URL, Action: "close"}, want: ErrPRNotFound},
		{name: "bad action", store: &fakeActionStore{pr: pr, ok: true}, request: StateRequest{PRID: "42", PRURL: pr.URL, Action: "merge"}, want: ErrInvalidAction},
		{name: "bad id", store: &fakeActionStore{pr: pr, ok: true}, request: StateRequest{PRID: "x", PRURL: pr.URL, Action: "close"}, want: ErrInvalidPR},
		{name: "provider not found", store: &fakeActionStore{pr: pr, ok: true}, request: StateRequest{PRID: "42", PRURL: pr.URL, Action: "close"}, scmErr: ports.ErrSCMNotFound, want: ErrPRNotFound},
		{name: "provider refused", store: &fakeActionStore{pr: pr, ok: true}, request: StateRequest{PRID: "42", PRURL: pr.URL, Action: "close"}, scmErr: ports.ErrSCMRejected, want: ErrPRRejected},
		{name: "unsupported", store: &fakeActionStore{pr: pr, ok: true}, request: StateRequest{PRID: "42", PRURL: pr.URL, Action: "draft"}, scmErr: ports.ErrSCMUnsupported, want: ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeStateWriter{}
			svc := NewActionService(ActionDeps{Store: tc.store, StateChanger: &fakeStateCommentSCM{stateErr: tc.scmErr}, StateWriter: writer})
			if _, err := svc.ChangeState(context.Background(), tc.request); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if writer.calls != 0 {
				t.Fatalf("state persisted after a failure")
			}
		})
	}
}

func TestActionServiceChangeState_LocalWriteFailureStillReportsSuccess(t *testing.T) {
	pr := openPRFixture()
	lifecycle := &fakeActionLifecycle{}
	svc := NewActionService(ActionDeps{
		Store: &fakeActionStore{pr: pr, ok: true}, Reader: &fakeSCMAction{}, StateChanger: &fakeStateCommentSCM{},
		StateWriter: &fakeStateWriter{err: errors.New("database locked")}, Lifecycle: lifecycle,
	})
	result, err := svc.ChangeState(context.Background(), StateRequest{PRID: "42", PRURL: pr.URL, Action: "close"})
	if err != nil || result.State != domain.PRStateClosed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("lifecycle called without a durable state")
	}
}

func TestActionServicePostComment_TopLevelStoresConversationAndRefreshes(t *testing.T) {
	pr := openPRFixture()
	createdAt := time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC)
	scm := &fakeStateCommentSCM{commentResult: ports.SCMCommentResult{ID: "IC_1", URL: "https://x/c1", Author: "me", Body: "Ship it", CreatedAt: createdAt}}
	writer := &fakeCommentWriter{}
	refresher := &fakeRefresher{}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Commenter: scm, CommentWriter: writer, Refresher: refresher})
	result, err := svc.PostComment(context.Background(), CommentRequest{PRID: "42", PRURL: pr.URL, Body: "Ship it"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "IC_1" || result.URL != "https://x/c1" || !result.CreatedAt.Equal(createdAt) {
		t.Fatalf("result = %#v", result)
	}
	if len(scm.commentRequests) != 1 || scm.commentRequests[0].ThreadID != "" || scm.commentRequests[0].Body != "Ship it" {
		t.Fatalf("requests = %#v", scm.commentRequests)
	}
	if len(writer.conversation) != 1 || writer.conversation[0].ID != "IC_1" || writer.conversation[0].Author != "me" || len(writer.reviewComments) != 0 {
		t.Fatalf("writer = %#v", writer)
	}
	if len(refresher.urls) != 1 || refresher.urls[0] != pr.URL {
		t.Fatalf("refresh = %#v", refresher.urls)
	}
}

func TestActionServicePostComment_ThreadReplyStoresReviewComment(t *testing.T) {
	pr := openPRFixture()
	store := &fakeActionStore{
		pr: pr, ok: true,
		threads:  []domain.PullRequestReviewThread{{ThreadID: "PRRT_1", Path: "main.go", Line: 12}},
		comments: []domain.PullRequestComment{{ThreadID: "PRRT_1", ID: "PRRC_0", AutoInjectReview: true}},
	}
	scm := &fakeStateCommentSCM{commentResult: ports.SCMCommentResult{ID: "PRRC_1", URL: "https://x/r1", Author: "me", Body: "Done"}}
	writer := &fakeCommentWriter{}
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	svc := NewActionService(ActionDeps{Store: store, Commenter: scm, CommentWriter: writer, Clock: func() time.Time { return now }})
	result, err := svc.PostComment(context.Background(), CommentRequest{PRID: "42", PRURL: pr.URL, Body: "Done", ThreadID: "PRRT_1"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.CreatedAt.Equal(now) {
		t.Fatalf("createdAt = %s, want clock fallback %s", result.CreatedAt, now)
	}
	if len(scm.commentRequests) != 1 || scm.commentRequests[0].ThreadID != "PRRT_1" {
		t.Fatalf("requests = %#v", scm.commentRequests)
	}
	want := domain.PullRequestComment{ThreadID: "PRRT_1", ID: "PRRC_1", Author: "me", File: "main.go", Line: 12, Body: "Done", URL: "https://x/r1", CreatedAt: now, AutoInjectReview: true}
	if len(writer.reviewComments) != 1 || writer.reviewComments[0] != want {
		t.Fatalf("stored = %#v, want %#v", writer.reviewComments, want)
	}
}

func TestActionServicePostComment_ThreadMustBelongToPR(t *testing.T) {
	pr := openPRFixture()
	scm := &fakeStateCommentSCM{}
	reader := &fakeSCMAction{review: ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{{ID: "PRRT_live"}}}}
	writer := &fakeCommentWriter{}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: reader, Commenter: scm, CommentWriter: writer})

	if _, err := svc.PostComment(context.Background(), CommentRequest{PRID: "42", PRURL: pr.URL, Body: "x", ThreadID: "PRRT_other"}); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("error = %v, want thread not found", err)
	}
	if len(scm.commentRequests) != 0 {
		t.Fatalf("posted to a foreign thread")
	}
	// A thread only the live listing knows is accepted but not stored early.
	scm.commentResult = ports.SCMCommentResult{ID: "PRRC_9"}
	if _, err := svc.PostComment(context.Background(), CommentRequest{PRID: "42", PRURL: pr.URL, Body: "x", ThreadID: "PRRT_live"}); err != nil {
		t.Fatal(err)
	}
	if len(scm.commentRequests) != 1 || len(writer.reviewComments) != 0 {
		t.Fatalf("requests=%d stored=%d", len(scm.commentRequests), len(writer.reviewComments))
	}
}

func TestActionServicePostComment_ValidationAndErrorMapping(t *testing.T) {
	pr := openPRFixture()
	for _, tc := range []struct {
		name    string
		store   *fakeActionStore
		request CommentRequest
		scmErr  error
		want    error
	}{
		{name: "blank body", store: &fakeActionStore{pr: pr, ok: true}, request: CommentRequest{PRID: "42", PRURL: pr.URL, Body: " \n\t"}, want: ErrInvalidComment},
		{name: "oversized body", store: &fakeActionStore{pr: pr, ok: true}, request: CommentRequest{PRID: "42", PRURL: pr.URL, Body: string(make([]byte, maxCommentBodyBytes+1))}, want: ErrInvalidComment},
		{name: "unknown pr", store: &fakeActionStore{}, request: CommentRequest{PRID: "42", PRURL: pr.URL, Body: "hi"}, want: ErrPRNotFound},
		{name: "provider refused", store: &fakeActionStore{pr: pr, ok: true}, request: CommentRequest{PRID: "42", PRURL: pr.URL, Body: "hi"}, scmErr: ports.ErrSCMRejected, want: ErrPRRejected},
		{name: "unsupported", store: &fakeActionStore{pr: pr, ok: true}, request: CommentRequest{PRID: "42", PRURL: pr.URL, Body: "hi"}, scmErr: ports.ErrSCMUnsupported, want: ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scm := &fakeStateCommentSCM{commentErr: tc.scmErr}
			svc := NewActionService(ActionDeps{Store: tc.store, Commenter: scm})
			if _, err := svc.PostComment(context.Background(), tc.request); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
