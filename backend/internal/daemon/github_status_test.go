package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeIdentity struct {
	identity ports.SCMIdentity
	err      error
	calls    int
}

func (f *fakeIdentity) AuthenticatedIdentityForProvider(context.Context, string, string) (ports.SCMIdentity, error) {
	f.calls++
	return f.identity, f.err
}

func TestGitHubStatusLoginIsTheHumanAccountOrEmpty(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	for name, tc := range map[string]struct {
		identity *fakeIdentity
		want     string
	}{
		"human":      {&fakeIdentity{identity: ports.SCMIdentity{Login: "octocat", Human: true}}, "octocat"},
		"bot":        {&fakeIdentity{identity: ports.SCMIdentity{Login: "ci-bot", Human: false}}, ""},
		"no login":   {&fakeIdentity{identity: ports.SCMIdentity{Human: true}}, ""},
		"lookup err": {&fakeIdentity{err: errors.New("gh: not logged in")}, ""},
	} {
		s := &githubStatusService{identity: tc.identity, now: clock}
		if got := s.Login(context.Background()); got != tc.want {
			t.Errorf("%s: Login = %q, want %q", name, got, tc.want)
		}
	}
	if got := (&githubStatusService{now: clock}).Login(context.Background()); got != "" {
		t.Errorf("no provider: Login = %q, want empty", got)
	}
}

func TestGitHubStatusLoginRemembersAMissBriefly(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	identity := &fakeIdentity{err: errors.New("gh: not logged in")}
	s := &githubStatusService{identity: identity, now: func() time.Time { return now }}

	s.Login(context.Background())
	s.Login(context.Background())
	if identity.calls != 1 {
		t.Fatalf("lookups within the miss window = %d, want 1", identity.calls)
	}
	now = now.Add(githubLoginMissTTL + time.Second)
	identity.err, identity.identity = nil, ports.SCMIdentity{Login: "octocat", Human: true}
	if got := s.Login(context.Background()); got != "octocat" || identity.calls != 2 {
		t.Fatalf("after the window: Login = %q after %d lookups, want octocat after 2", got, identity.calls)
	}
}
