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
	attachBatches   []ports.SCMAttachmentBatch
	discarded       []string
}

func (a *actingProvider) AttachmentAccess(context.Context, ports.SCMPRRef) (ports.SCMAttachmentAccess, error) {
	return ports.SCMAttachmentAccess{Allowed: true, Location: "o/r"}, nil
}

func (a *actingProvider) StoreAttachments(_ context.Context, batch ports.SCMAttachmentBatch) (ports.SCMStoredAttachments, error) {
	a.attachBatches = append(a.attachBatches, batch)
	return ports.SCMStoredAttachments{URLs: []string{"https://x/f.png"}, Handle: "refs/ao/attachments/pr-1/x"}, nil
}

func (a *actingProvider) DiscardAttachments(_ context.Context, _ ports.SCMPRRef, handle string) error {
	a.discarded = append(a.discarded, handle)
	return nil
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
	batch := ports.SCMAttachmentBatch{PR: ghRef, Files: []ports.SCMAttachmentFile{{Name: "f.png"}}}
	if got, err := m.StoreAttachments(context.Background(), batch); err != nil || len(got.URLs) != 1 || got.URLs[0] != "https://x/f.png" || len(gh.attachBatches) != 1 {
		t.Fatalf("attachments = %#v, err = %v, calls = %d", got, err, len(gh.attachBatches))
	}
	if access, err := m.AttachmentAccess(context.Background(), ghRef); err != nil || !access.Allowed {
		t.Fatalf("access = %#v, err = %v", access, err)
	}
	if err := m.DiscardAttachments(context.Background(), ghRef, "refs/ao/attachments/pr-1/x"); err != nil || len(gh.discarded) != 1 {
		t.Fatalf("discard err = %v, calls = %d", err, len(gh.discarded))
	}
	if _, err := m.StoreAttachments(context.Background(), ports.SCMAttachmentBatch{PR: glRef}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("attachment error = %v, want unsupported", err)
	}
	if _, err := nilProvider.AttachmentAccess(context.Background(), ghRef); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("nil provider attachment error = %v, want unsupported", err)
	}
}
