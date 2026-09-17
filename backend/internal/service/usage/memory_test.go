package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

type memStore struct{ recs []domain.SessionRecord }

func (s memStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return s.recs, nil
}
func (s memStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return s.recs, nil
}

type memRuntime struct{ roots map[string][]int }

func (r memRuntime) ProcessRootPIDs(_ context.Context, h ports.RuntimeHandle) ([]int, error) {
	if h.ID == "broken" {
		return nil, errors.New("probe failed")
	}
	return r.roots[h.ID], nil
}

const psTable = `
  100     1   900 tmux: server
  200   100  3000 bash
  300   200 1600000 claude
  400   100  2500 bash
`

func newTestMemoryReader(t *testing.T, recs []domain.SessionRecord, snapshots *int) *MemoryReader {
	t.Helper()
	return NewMemoryReader(MemoryReaderDeps{
		Store:   memStore{recs: recs},
		Runtime: memRuntime{roots: map[string][]int{"a": {200}, "b": {400}, "gone": {9999}}},
		Snapshot: func(context.Context) (*procmem.Table, error) {
			*snapshots++
			return procmem.Parse(psTable)
		},
		ChatHostPID: func(id domain.SessionID) (int, bool) {
			if id == "s-chat" {
				return 400, true
			}
			return 0, false
		},
		Now:      func() time.Time { return time.Unix(1000, 0) },
		CacheTTL: 2 * time.Second,
	})
}

func TestListMemoryMeasuresChatSessionsByHostPID(t *testing.T) {
	recs := []domain.SessionRecord{
		{ID: "s-chat", Mode: domain.SessionModeChat},
		{ID: "s-chat-gone", Mode: domain.SessionModeChat},
	}
	var snapshots int
	items, err := newTestMemoryReader(t, recs, &snapshots).ListMemory(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SessionID != "s-chat" || items[0].RSSBytes != 2500*1024 {
		t.Fatalf("items = %+v, want only s-chat at 2500 KiB", items)
	}
}

func TestListMemorySkipsTerminatedAndUnprobeable(t *testing.T) {
	recs := []domain.SessionRecord{
		{ID: "s-a", Metadata: domain.SessionMetadata{RuntimeHandleID: "a"}},
		{ID: "s-b", Metadata: domain.SessionMetadata{RuntimeHandleID: "b"}, IsTerminated: true},
		{ID: "s-none"},
		{ID: "s-broken", Metadata: domain.SessionMetadata{RuntimeHandleID: "broken"}},
		{ID: "s-gone", Metadata: domain.SessionMetadata{RuntimeHandleID: "gone"}},
	}
	var snapshots int
	items, err := newTestMemoryReader(t, recs, &snapshots).ListMemory(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SessionID != "s-a" {
		t.Fatalf("items = %+v, want only s-a", items)
	}
	if items[0].RSSBytes != (3000+1600000)*1024 || items[0].ProcessCount != 2 {
		t.Fatalf("reading = %+v", items[0])
	}
	if items[0].SampledAt != time.Unix(1000, 0) {
		t.Fatalf("sampledAt = %v", items[0].SampledAt)
	}
}

func TestListMemoryCachesProcessTable(t *testing.T) {
	recs := []domain.SessionRecord{{ID: "s-a", Metadata: domain.SessionMetadata{RuntimeHandleID: "a"}}}
	var snapshots int
	r := newTestMemoryReader(t, recs, &snapshots)
	for range 3 {
		if _, err := r.ListMemory(context.Background(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if snapshots != 1 {
		t.Fatalf("snapshots = %d, want 1 within cache ttl", snapshots)
	}
}

func TestListMemoryUnsupportedPassesThrough(t *testing.T) {
	r := NewMemoryReader(MemoryReaderDeps{
		Store: memStore{}, Runtime: memRuntime{},
		Snapshot: func(context.Context) (*procmem.Table, error) { return nil, procmem.ErrUnsupported },
	})
	if _, err := r.ListMemory(context.Background(), ""); !errors.Is(err, procmem.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
