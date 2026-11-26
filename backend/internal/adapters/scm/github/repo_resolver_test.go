package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRepoResolverFollowsRename(t *testing.T) {
	f := newFakeGH(t)
	// GitHub answers a renamed or transferred repository's old path with a
	// redirect to its immutable /repositories/{id} URL.
	f.on(http.MethodGet, "/repos/Untrivial-ai/agent-orchestrator", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/repositories/42", http.StatusMovedPermanently)
	})
	f.on(http.MethodGet, "/repositories/42", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "full_name": "OrchestratorInc/agent-orchestrator", "owner": map[string]any{"avatar_url": "https://avatars.githubusercontent.com/u/310302959?v=4"}})
	})
	r := NewRepoResolver(newProviderForTest(t, f))

	got, err := r.ResolveGitHubRepository(ctx(), "Untrivial-ai", "agent-orchestrator.git")
	if err != nil {
		t.Fatal(err)
	}
	if got != (ports.GitHubRepository{ID: 42, FullName: "OrchestratorInc/agent-orchestrator", OwnerAvatarURL: "https://avatars.githubusercontent.com/u/310302959?v=4"}) {
		t.Fatalf("resolved = %#v", got)
	}
	if _, err := r.ResolveGitHubRepository(ctx(), "untrivial-ai", "Agent-Orchestrator"); err != nil {
		t.Fatal(err)
	}
	if n := f.callsTo(http.MethodGet, "/repos/Untrivial-ai/agent-orchestrator"); n != 1 {
		t.Fatalf("lookups = %d, want 1 (cached, case-insensitive)", n)
	}
}

func TestRepoResolverUnchangedRepository(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/acme/widgets", func(w http.ResponseWriter, _ *http.Request) {
		// An avatar on any other host is dropped: the renderer only loads
		// GitHub avatars from avatars.githubusercontent.com.
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": "acme/widgets", "owner": map[string]any{"avatar_url": "https://evil.example/acme.png"}})
	})
	r := NewRepoResolver(newProviderForTest(t, f))

	got, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if got != (ports.GitHubRepository{ID: 7, FullName: "acme/widgets"}) {
		t.Fatalf("resolved = %#v", got)
	}
}

func TestRepoResolverFailureIsCachedBriefly(t *testing.T) {
	f := newFakeGH(t)
	var failing atomic.Bool
	failing.Store(true)
	f.on(http.MethodGet, "/repos/acme/widgets", func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": "acme/widgets"})
	})
	r := NewRepoResolver(newProviderForTest(t, f))
	now := time.Unix(1_000_000, 0)
	r.now = func() time.Time { return now }

	if _, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets"); err == nil {
		t.Fatal("expected an error from a failing API")
	}
	if _, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets"); err == nil {
		t.Fatal("expected the cached failure")
	}
	if n := f.callsTo(http.MethodGet, "/repos/acme/widgets"); n != 1 {
		t.Fatalf("lookups = %d, want 1 while the failure is cached", n)
	}

	failing.Store(false)
	now = now.Add(repoResolveFailureTTL + time.Second)
	got, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets")
	if err != nil || got.FullName != "acme/widgets" {
		t.Fatalf("after failure TTL: %#v, %v", got, err)
	}
}

func TestRepoResolverNotFound(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/gone/repo", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	r := NewRepoResolver(newProviderForTest(t, f))

	if _, err := r.ResolveGitHubRepository(ctx(), "gone", "repo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRepoResolverCallerDeadlineDoesNotCancelLookup(t *testing.T) {
	f := newFakeGH(t)
	release := make(chan struct{})
	f.on(http.MethodGet, "/repos/acme/widgets", func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": "acme/widgets"})
	})
	r := NewRepoResolver(newProviderForTest(t, f))

	short, cancel := context.WithTimeout(ctx(), 10*time.Millisecond)
	defer cancel()
	if _, err := r.ResolveGitHubRepository(short, "acme", "widgets"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	close(release)
	got, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets")
	if err != nil || got.ID != 7 {
		t.Fatalf("resolved = %#v, %v", got, err)
	}
	if n := f.callsTo(http.MethodGet, "/repos/acme/widgets"); n != 1 {
		t.Fatalf("lookups = %d, want 1 (the detached lookup is shared)", n)
	}
}

func TestRepoResolverKeepsLastGoodAnswerOnTransientFailure(t *testing.T) {
	f := newFakeGH(t)
	var status atomic.Int32
	status.Store(http.StatusOK)
	f.on(http.MethodGet, "/repos/acme/widgets", func(w http.ResponseWriter, _ *http.Request) {
		if code := int(status.Load()); code != http.StatusOK {
			http.Error(w, `{"message":"nope"}`, code)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": "acme/widgets"})
	})
	r := NewRepoResolver(newProviderForTest(t, f))
	now := time.Unix(1_000_000, 0)
	r.now = func() time.Time { return now }
	if _, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets"); err != nil {
		t.Fatal(err)
	}

	status.Store(http.StatusBadGateway)
	now = now.Add(repoResolveTTL + time.Second)
	got, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets")
	if err != nil || got.ID != 7 {
		t.Fatalf("after a transient failure: %#v, %v; want the last good answer", got, err)
	}

	status.Store(http.StatusNotFound)
	now = now.Add(repoResolveFailureTTL + time.Second)
	if _, err := r.ResolveGitHubRepository(ctx(), "acme", "widgets"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after a 404: err = %v, want ErrNotFound", err)
	}
}
