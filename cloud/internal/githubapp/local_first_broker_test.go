package githubapp

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// unauthorizedCheckoutStore is a session the local App does not authorize.
type unauthorizedCheckoutStore struct{ checkoutStubStore }

func (*unauthorizedCheckoutStore) WorkerGitHubCheckoutContext(context.Context, string, string) (domain.GitHubCheckoutContext, error) {
	return domain.GitHubCheckoutContext{}, postgres.ErrForbidden
}

func (*unauthorizedCheckoutStore) WorkerRemoteGitHubCheckoutContext(context.Context, string, string) (domain.RemoteGitHubCheckoutContext, error) {
	return domain.RemoteGitHubCheckoutContext{}, postgres.ErrForbidden
}

// remoteLookupStore reports that the remote broker was consulted.
type remoteLookupStore struct{}

func (remoteLookupStore) WorkerRemoteGitHubCheckoutContext(context.Context, string, string) (domain.RemoteGitHubCheckoutContext, error) {
	return domain.RemoteGitHubCheckoutContext{}, errRemoteLookup
}

var errRemoteLookup = errors.New("remote broker consulted")

// Staging's own App serves what it authorizes; a session it does not
// authorize is read through the remote broker, and never pushed through it.
func TestLocalFirstBrokerFallsBackToTheRemoteBrokerForReadsOnly(t *testing.T) {
	store := &unauthorizedCheckoutStore{}
	local := newCheckoutTestService(t, store, newAppTestClient(t, "http://127.0.0.1:1", nil))
	remote := &RemoteCheckoutBroker{store: remoteLookupStore{}}
	broker := LocalFirstBroker{Service: local, Remote: remote}

	if _, err := broker.IssueCheckoutGrant(context.Background(), "org-1", "sess-1"); !errors.Is(err, errRemoteLookup) {
		t.Fatalf("checkout err = %v, want the remote broker consulted", err)
	}
	if _, err := broker.IssueGitProxyGrant(context.Background(), "org-1", "sess-1", "octo/app", false); !errors.Is(err, errRemoteLookup) {
		t.Fatalf("fetch err = %v, want the remote broker consulted", err)
	}
	if _, err := broker.IssueGitProxyGrant(context.Background(), "org-1", "sess-1", "octo/app", true); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("push err = %v, want the local refusal (no remote pushes)", err)
	}
}
