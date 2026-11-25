package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type editingProvider struct {
	fakeProvider
	edits []ports.SCMPREditRequest
}

func (e *editingProvider) PullRequestContent(context.Context, ports.SCMPRRef) (ports.SCMPRContent, error) {
	return ports.SCMPRContent{Title: "current", CanEdit: true}, nil
}

func (e *editingProvider) EditPullRequest(_ context.Context, request ports.SCMPREditRequest) (ports.SCMPRContent, error) {
	e.edits = append(e.edits, request)
	return ports.SCMPRContent{Title: *request.Title}, nil
}

func TestPREditsRouteByProviderAndReportUnsupported(t *testing.T) {
	gh := &editingProvider{fakeProvider: fakeProvider{key: "github"}}
	gl := &fakeProvider{key: "gitlab"}
	m := New(NamedProvider{Key: "github", Provider: gh}, NamedProvider{Key: "gitlab", Provider: gl})
	ghRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Owner: "o", Name: "r"}, Number: 1}
	glRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Owner: "o", Name: "r"}, Number: 1}
	title := "new"

	if got, err := m.PullRequestContent(context.Background(), ghRef); err != nil || got.Title != "current" {
		t.Fatalf("content = %#v, err %v", got, err)
	}
	if got, err := m.EditPullRequest(context.Background(), ports.SCMPREditRequest{PR: ghRef, Title: &title}); err != nil || got.Title != "new" || len(gh.edits) != 1 {
		t.Fatalf("edit = %#v, err %v, edits %d", got, err, len(gh.edits))
	}
	if _, err := m.PullRequestContent(context.Background(), glRef); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("content error = %v, want unsupported", err)
	}
	if _, err := m.EditPullRequest(context.Background(), ports.SCMPREditRequest{PR: glRef, Title: &title}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("edit error = %v, want unsupported", err)
	}
	var nilProvider *Provider
	if _, err := nilProvider.EditPullRequest(context.Background(), ports.SCMPREditRequest{PR: ghRef, Title: &title}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("nil provider error = %v, want unsupported", err)
	}
}
