package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestPrefetchHandsItsValueToOneConsumer(t *testing.T) {
	p := startPrefetch(context.Background(), time.Minute, func(context.Context) (string, error) {
		return "secret", nil
	})
	var wg sync.WaitGroup
	var got atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if value, ok := p.take(context.Background()); ok && value == "secret" {
				got.Add(1)
			}
		}()
	}
	wg.Wait()
	if got.Load() != 1 {
		t.Fatalf("%d consumers got the prefetched value, want exactly 1", got.Load())
	}
	if p.value != "" {
		t.Fatal("prefetched value kept after its single use")
	}
}

func TestPrefetchFallsBackOnFailureOrAge(t *testing.T) {
	failed := startPrefetch(context.Background(), time.Minute, func(context.Context) (string, error) {
		return "", errors.New("boom")
	})
	if _, ok := failed.take(context.Background()); ok {
		t.Fatal("a failed lookup must make the caller fetch for itself")
	}
	stale := startPrefetch(context.Background(), time.Nanosecond, func(context.Context) (string, error) {
		return "old", nil
	})
	<-stale.done
	time.Sleep(time.Millisecond)
	if _, ok := stale.take(context.Background()); ok {
		t.Fatal("a stale lookup must make the caller fetch for itself")
	}
	var none *prefetch[string]
	if _, ok := none.take(context.Background()); ok {
		t.Fatal("a nil prefetch must make the caller fetch for itself")
	}
}

func TestPrefetchHonoursTheCallersContext(t *testing.T) {
	release := make(chan struct{})
	p := startPrefetch(context.Background(), time.Minute, func(context.Context) (string, error) {
		<-release
		return "late", nil
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := p.take(ctx); ok {
		t.Fatal("a caller whose context ended must not wait for the lookup")
	}
}

// The first credential request uses the lookup made beside checkout; later
// ones ask the control plane again, so a relaunch never reuses a credential.
func TestCredentialUsesThePrefetchOnceThenFetches(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/worker/credential" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"provider":"anthropic","credentialType":"oauth","secret":"s"}`)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client()}
	c.credentialPrefetch = startPrefetch(context.Background(), time.Minute, func(ctx context.Context) (worker.CredentialResponse, error) {
		return c.agentCredential(ctx, "")
	})
	<-c.credentialPrefetch.done

	for want := int32(1); want <= 3; want++ {
		if _, err := c.Credential(context.Background()); err != nil {
			t.Fatalf("credential: %v", err)
		}
		// The prefetch made call 1 and the first Credential used it; each later
		// Credential makes its own request.
		if want > 1 && calls.Load() != want {
			t.Fatalf("after %d credential requests the control plane saw %d calls", want, calls.Load())
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("control plane saw %d credential calls, want 3", calls.Load())
	}
}

// A transcript found by the prefetch is what rehydration reads, and a second
// lookup goes to the control plane again.
func TestGetTranscriptUsesThePrefetchOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.NotFound(w, nil)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client()}
	c.transcriptPrefetch = startPrefetch(context.Background(), time.Minute, func(ctx context.Context) (transcriptLookup, error) {
		checkpoint, found, err := c.fetchTranscript(ctx)
		return transcriptLookup{checkpoint: checkpoint, found: found}, err
	})
	if _, found, err := c.getTranscript(context.Background()); err != nil || found {
		t.Fatalf("first lookup: found=%v err=%v; want nothing captured", found, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("first lookup made %d calls, want only the prefetch", calls.Load())
	}
	if _, _, err := c.getTranscript(context.Background()); err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("second lookup did not ask the control plane: %d calls", calls.Load())
	}
}
