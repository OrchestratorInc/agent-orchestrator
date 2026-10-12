package billing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeUsageStore struct {
	orgs      []string
	sampleErr error
	paused    map[string]int
	failOrg   string

	usageBefore, eventsBefore time.Time
	pruneEventsErr            error
}

func (f *fakeUsageStore) SampleUsage(context.Context) ([]string, error) { return f.orgs, f.sampleErr }
func (f *fakeUsageStore) PauseOverLimit(_ context.Context, orgID string) (int64, error) {
	if orgID == f.failOrg {
		return 0, errors.New("transient")
	}
	f.paused[orgID]++
	return 1, nil
}
func (f *fakeUsageStore) PruneUsage(_ context.Context, before time.Time) error {
	f.usageBefore = before
	return nil
}
func (f *fakeUsageStore) PruneStripeEvents(_ context.Context, before time.Time) error {
	f.eventsBefore = before
	return f.pruneEventsErr
}

func TestPruneDropsOldUsageAndProcessedEvents(t *testing.T) {
	store := &fakeUsageStore{paused: map[string]int{}}
	enforcer := &Enforcer{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	if !enforcer.Prune(context.Background(), now) {
		t.Fatal("prune failed")
	}
	if !store.usageBefore.Equal(now.Add(-usageRetention)) || !store.eventsBefore.Equal(now.Add(-90*24*time.Hour)) {
		t.Fatalf("usage before %v, events before %v", store.usageBefore, store.eventsBefore)
	}
	store.pruneEventsErr = errors.New("db down")
	if enforcer.Prune(context.Background(), now) {
		t.Fatal("a failed event prune must be retried")
	}
}

func TestTickEnforcesEveryOrgEvenWhenOneFails(t *testing.T) {
	store := &fakeUsageStore{orgs: []string{"a", "b", "c"}, paused: map[string]int{}, failOrg: "b"}
	(&Enforcer{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Tick(context.Background())
	if store.paused["a"] != 1 || store.paused["c"] != 1 {
		t.Fatalf("paused = %v; one org's failure must not skip the others", store.paused)
	}
}

func TestTickSkipsEnforcementWhenSamplingFails(t *testing.T) {
	store := &fakeUsageStore{orgs: []string{"a"}, sampleErr: errors.New("db down"), paused: map[string]int{}}
	(&Enforcer{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Tick(context.Background())
	if len(store.paused) != 0 {
		t.Fatal("enforced on a failed sample")
	}
}
