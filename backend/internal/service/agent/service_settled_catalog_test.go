package agent

import (
	"context"
	"errors"
	"fmt"
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
	if !svc.claimSettledRediscovery("cursor", "") {
		t.Fatal("the first read after a settled failure must rediscover")
	}
	if svc.claimSettledRediscovery("cursor", "") {
		t.Fatal("a second read within the floor rediscovered again")
	}
	now = now.Add(modelCatalogSettledReadFloor)
	if !svc.claimSettledRediscovery("cursor", "") {
		t.Fatal("a read after the floor did not rediscover")
	}
	// An ordinary failure has its own timed retries and no read rediscovery.
	svc.markSettledCatalog("cursor", "", false)
	now = now.Add(time.Hour)
	if svc.claimSettledRediscovery("cursor", "") {
		t.Fatal("a non-settled failure was rediscovered on read")
	}
}
