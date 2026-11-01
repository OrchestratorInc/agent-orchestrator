package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// actingProvider is a fakeProvider that also supports PR state and comments.
type actingProvider struct {
	fakeProvider
	stateRequests   []ports.SCMPRStateRequest
	commentRequests []ports.SCMCommentRequest
}

func (a *actingProvider) ChangePullRequestState(_ context.Context, request ports.SCMPRStateRequest) error {
	a.stateRequests = append(a.stateRequests, request)
	return nil
}

func (a *actingProvider) PostComment(_ context.Context, request ports.SCMCommentRequest) (ports.SCMCommentResult, error) {
	a.commentRequests = append(a.commentRequests, request)
	return ports.SCMCommentResult{ID: "c1"}, nil
}

func TestPRActionsRouteByProviderAndReportUnsupported(t *testing.T) {
	gh := &actingProvider{fakeProvider: fakeProvider{key: "github"}}
	gl := &fakeProvider{key: "gitlab"}
	m := New(NamedProvider{Key: "github", Provider: gh}, NamedProvider{Key: "gitlab", Provider: gl})

	ghRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github"}, Number: 1}
	if err := m.ChangePullRequestState(context.Background(), ports.SCMPRStateRequest{PR: ghRef, Action: ports.SCMPRClose}); err != nil {
		t.Fatal(err)
	}
	if got, err := m.PostComment(context.Background(), ports.SCMCommentRequest{PR: ghRef, Body: "hi"}); err != nil || got.ID != "c1" {
		t.Fatalf("comment = %#v, err = %v", got, err)
	}
	if len(gh.stateRequests) != 1 || len(gh.commentRequests) != 1 {
		t.Fatalf("github saw state=%d comments=%d", len(gh.stateRequests), len(gh.commentRequests))
	}

	glRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab"}, Number: 1}
	if err := m.ChangePullRequestState(context.Background(), ports.SCMPRStateRequest{PR: glRef, Action: ports.SCMPRClose}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("state error = %v, want unsupported", err)
	}
	if _, err := m.PostComment(context.Background(), ports.SCMCommentRequest{PR: glRef, Body: "hi"}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("comment error = %v, want unsupported", err)
	}
	var nilProvider *Provider
	if err := nilProvider.ChangePullRequestState(context.Background(), ports.SCMPRStateRequest{PR: ghRef}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("nil provider error = %v, want unsupported", err)
	}
}
