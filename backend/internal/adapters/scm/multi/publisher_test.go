package multi

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePublisherProvider struct {
	fakeProvider
	called int
}

func (f *fakePublisherProvider) ReconcileOrCreatePullRequest(_ context.Context, request ports.SCMPublishRequest) (ports.SCMPublishResult, error) {
	f.called++
	return ports.SCMPublishResult{PR: ports.SCMPRObservation{Number: 9, SourceBranch: request.SourceBranch}}, nil
}

func TestProviderRoutesPullRequestPublication(t *testing.T) {
	gh := &fakePublisherProvider{fakeProvider: fakeProvider{key: "github"}}
	gl := &fakePublisherProvider{fakeProvider: fakeProvider{key: "gitlab"}}
	p := New(NamedProvider{Key: "github", Provider: gh}, NamedProvider{Key: "gitlab", Provider: gl})
	got, err := p.ReconcileOrCreatePullRequest(context.Background(), ports.SCMPublishRequest{Repo: ports.SCMRepo{Provider: "gitlab"}, SourceBranch: "ao/card"})
	if err != nil {
		t.Fatal(err)
	}
	if got.PR.Number != 9 || gh.called != 0 || gl.called != 1 {
		t.Fatalf("result=%#v calls=%d/%d", got, gh.called, gl.called)
	}
}
