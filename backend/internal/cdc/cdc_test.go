package cdc_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedSession(t *testing.T, s *sqlite.Store) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/m", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestE2E_StoreWriteToBroadcast(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := seedSession(t, s)

	r.Activity.State = domain.ActivityIdle
	if err := s.UpdateSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.WritePR(ctx, domain.PullRequest{URL: "pr1", SessionID: r.ID, UpdatedAt: r.UpdatedAt}, nil, nil); err != nil {
		t.Fatal(err)
	}

	var got []cdc.Event
	bc := cdc.NewBroadcaster()
	bc.Subscribe(func(e cdc.Event) { got = append(got, e) })
	p := cdc.NewPoller(s, bc, cdc.PollerConfig{})
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("delivered %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d seq=%d, want %d", i, e.Seq, i+1)
		}
		if e.ProjectID != "mer" {
			t.Fatalf("event %d project=%q, want mer", i, e.ProjectID)
		}
	}
	if got[0].Type != cdc.EventSessionCreated || got[1].Type != cdc.EventSessionUpdated || got[2].Type != cdc.EventPRCreated {
		t.Fatalf("types = %s, %s, %s", got[0].Type, got[1].Type, got[2].Type)
	}
	var payload map[string]any
	if err := json.Unmarshal(got[0].Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["id"] != string(r.ID) || payload["activity"] != "active" {
		t.Fatalf("payload = %v", payload)
	}

	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("re-poll delivered extra events: %d", len(got))
	}
}

func TestE2E_ConcurrentPollerLiveDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newStore(t)
	r := seedSession(t, s)

	var mu sync.Mutex
	var got []cdc.Event
	bc := cdc.NewBroadcaster()
	bc.Subscribe(func(e cdc.Event) { mu.Lock(); got = append(got, e); mu.Unlock() })

	p := cdc.NewPoller(s, bc, cdc.PollerConfig{})
	done := p.Start(ctx)

	const n = 6
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			r.Activity.State = domain.ActivityActive
		} else {
			r.Activity.State = domain.ActivityIdle
		}
		if err := s.UpdateSession(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	want := n

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		c := len(got)
		mu.Unlock()
		if c >= want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: delivered %d/%d", c, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) != want {
		t.Fatalf("delivered %d events, want %d", len(got), want)
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, want %d (out-of-order/duplicate)", i, e.Seq, i+1)
		}
	}
}

func TestBroadcasterRecoversPanickingSubscriber(t *testing.T) {
	bc := cdc.NewBroadcaster()
	good := 0
	bc.Subscribe(func(cdc.Event) { panic("boom") })
	bc.Subscribe(func(cdc.Event) { good++ })

	bc.Publish(cdc.Event{Seq: 1})
	bc.Publish(cdc.Event{Seq: 2})

	if good != 2 {
		t.Fatalf("good subscriber got %d, want 2 (panic was not isolated)", good)
	}
}
