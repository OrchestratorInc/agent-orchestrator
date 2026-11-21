package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestModelCatalogFailureIsSettled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("cline %w", ports.ErrAgentModelConfigurationMissing), true},
		{fmt.Errorf("discover: %w", ports.ErrAgentModelDiscoveryCredentialRejected), true},
		{fmt.Errorf("discover: %w", ports.ErrAgentModelDiscoveryCredentialExpired), true},
		{errors.New("auggie model discovery: exit status 1: You are not currently logged in to Augment"), true},
		{errors.New("cursor model discovery: exit status 1: Error: Authentication required. Run 'agent login'"), true},
		{errors.New("agy model discovery: exit status 41: Please set an Auth method in your settings.json"), true},
		{errors.New("claude-code model discovery: the configured gateway returned HTTP 404"), false},
		{errors.New("codex model discovery: context deadline exceeded"), false},
		{errors.New("codex model discovery: HTTP 407 Proxy Authentication Required"), false},
		{errors.New("claude-code model discovery: gateway answered: authentication required, upstream timed out"), false},
		{errors.New("opencode model discovery: HTTP 503 Service Unavailable: authentication required"), false},
		{errors.New("cursor model discovery: status 502: please sign in again later"), false},
		{errors.New("cursor model discovery: HTTP 401: authentication required"), true},
		{nil, false},
	}
	for _, tc := range cases {
		if got := modelCatalogFailureIsSettled(tc.err); got != tc.want {
			t.Errorf("modelCatalogFailureIsSettled(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// A catalog that failed because the agent is signed out gets no background
// retries; the next read (a picker opening) rediscovers it, so a sign-in done
// in a terminal shows the next time someone looks. Reads within the floor
// do not run the CLI again.
func TestReadRediscoversASettledCatalogFailure(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("auggie model discovery: exit status 1: You are not currently logged in to Augment")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("auggie", "Auggie", nil)}, &fakeModelCache{}, discoverer)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	if _, err := svc.Models(context.Background(), "auggie", "", true); err != nil {
		t.Fatal(err)
	}
	waitCalls := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for discoverer.discoverCalls.Load() < want && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := discoverer.discoverCalls.Load(); got != want {
			t.Fatalf("discoveries = %d, want %d", got, want)
		}
	}
	waitCalls(1)

	// The user signs in from a terminal, then opens a picker.
	discoverer.mu.Lock()
	discoverer.err = nil
	discoverer.mu.Unlock()
	read, err := svc.Models(context.Background(), "auggie", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if read.RefreshState != "refreshing" {
		t.Fatalf("read of a settled failure reports %q, want refreshing so open views re-read", read.RefreshState)
	}
	waitCalls(2)
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := svc.Models(context.Background(), "auggie", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Models) == 1 && got.Models[0].ID == "model-one" && got.RefreshState != "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog after signing in = %#v, want model-one", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSettledRediscoveryWaitsForTheFloorBetweenReads(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("cursor model discovery: exit status 1: Error: Authentication required. Run 'agent login'")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("cursor", "Cursor", nil)}, &fakeModelCache{}, discoverer)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	if _, err := svc.Models(context.Background(), "cursor", "", true); err != nil {
		t.Fatal(err)
	}
	if !svc.claimSettledRediscovery("cursor", "", false) {
		t.Fatal("the first read after a settled failure must rediscover")
	}
	if svc.claimSettledRediscovery("cursor", "", false) {
		t.Fatal("a second read within the floor rediscovered again")
	}
	now = now.Add(modelCatalogSettledReadFloor)
	if !svc.claimSettledRediscovery("cursor", "", false) {
		t.Fatal("a read after the floor did not rediscover")
	}
	// An ordinary failure has its own timed retries and no read rediscovery.
	svc.markSettledCatalog("cursor", "", false)
	now = now.Add(time.Hour)
	if svc.claimSettledRediscovery("cursor", "", false) {
		t.Fatal("a non-settled failure was rediscovered on read")
	}
}

// The settled mark is in memory, but a saved catalog keeps its failure text: a
// daemon that restarts after a settled failure still rediscovers it on read.
func TestReadRediscoversASettledFailureAfterARestart(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("auggie model discovery: exit status 1: You are not currently logged in to Augment")
	cache := &fakeModelCache{}
	agents := []agentregistry.HarnessAgent{harnessAgent("auggie", "Auggie", nil)}
	first := newService(agents, cache, discoverer)
	if _, err := first.Models(context.Background(), "auggie", "", true); err != nil {
		t.Fatal(err)
	}

	restarted := newService(agents, cache, discoverer)
	discoverer.mu.Lock()
	discoverer.err = nil
	discoverer.mu.Unlock()
	read, err := restarted.Models(context.Background(), "auggie", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if read.RefreshState != "refreshing" {
		t.Fatalf("read after a restart reports %q, want refreshing", read.RefreshState)
	}
}

// A catalog that still shows its models keeps the settled failure's code while
// the next failure is saved, so a timeout after an expired login (text that no
// settled phrase matches) gets the timed retries back.
func TestTimeoutAfterAnExpiredLoginWithModelsRetriesAgain(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("auggie", "Auggie", nil)}, &fakeModelCache{}, discoverer)
	if _, err := svc.Models(context.Background(), "auggie", "", true); err != nil {
		t.Fatal(err)
	}
	discoverer.mu.Lock()
	discoverer.err = wrapAuthError(ports.ErrAgentModelDiscoveryCredentialExpired, "the saved login token expired; it renews the next time it runs")
	discoverer.mu.Unlock()
	if _, err := svc.Models(context.Background(), "auggie", "", true); err != nil {
		t.Fatal(err)
	}
	discoverer.mu.Lock()
	discoverer.err = errors.New("auggie model discovery: context deadline exceeded")
	discoverer.mu.Unlock()
	if _, err := svc.Models(context.Background(), "auggie", "", false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := svc.Models(context.Background(), "auggie", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if got.RefreshState == "error" && strings.Contains(got.RefreshError, "deadline exceeded") {
			if got.RetryAt == nil {
				t.Fatalf("a timeout after an expired login scheduled no retry: %#v", got)
			}
			if len(got.Models) == 0 {
				t.Fatalf("the saved models were dropped: %#v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the read did not rediscover: calls=%d state=%q err=%q", discoverer.discoverCalls.Load(), got.RefreshState, got.RefreshError)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An expired login is saved with provider wording that names no sentinel; its
// sign-in code still marks it settled, so a read after a restart rediscovers.
func TestReadRediscoversAnExpiredLoginAfterARestart(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	discoverer.err = wrapAuthError(ports.ErrAgentModelDiscoveryCredentialExpired, "the saved login token expired; it renews the next time it runs")
	cache := &fakeModelCache{}
	agents := []agentregistry.HarnessAgent{harnessAgent("auggie", "Auggie", nil)}
	if _, err := newService(agents, cache, discoverer).Models(context.Background(), "auggie", "", true); err != nil {
		t.Fatal(err)
	}
	discoverer.mu.Lock()
	discoverer.err = nil
	discoverer.mu.Unlock()
	read, err := newService(agents, cache, discoverer).Models(context.Background(), "auggie", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if read.RefreshState != "refreshing" {
		t.Fatalf("read after a restart reports %q, want refreshing", read.RefreshState)
	}
}

// A settled failure spends the retry budget at once. When the rediscovery a
// read starts then fails differently (a timeout, say), the normal timed
// retries come back.
func TestTransientFailureAfterASettledOneRetriesAgain(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("auggie model discovery: exit status 1: You are not currently logged in to Augment")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("auggie", "Auggie", nil)}, &fakeModelCache{}, discoverer)
	if _, err := svc.Models(context.Background(), "auggie", "", true); err != nil {
		t.Fatal(err)
	}
	discoverer.mu.Lock()
	discoverer.err = errors.New("auggie model discovery: context deadline exceeded")
	discoverer.mu.Unlock()
	if _, err := svc.Models(context.Background(), "auggie", "", false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := svc.Models(context.Background(), "auggie", "", false)
		if err != nil {
			t.Fatal(err)
		}
		// Wait for the timeout itself to be saved, not a read of the settled one.
		if got.RefreshState == "error" && strings.Contains(got.RefreshError, "deadline exceeded") {
			if got.RetryAt == nil {
				t.Fatalf("a timeout after a settled failure scheduled no retry: %#v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the read did not rediscover: calls=%d state=%q", discoverer.discoverCalls.Load(), got.RefreshState)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
