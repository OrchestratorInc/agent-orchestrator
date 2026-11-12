package pr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeDetailsStore struct {
	stored    bool
	saved     []domain.PullRequestDetails
	savedURL  string
	updatedAt time.Time
}

func (f *fakeDetailsStore) GetPRDetails(context.Context, string) (domain.PullRequestDetails, bool, error) {
	return domain.PullRequestDetails{}, f.stored, nil
}

func (f *fakeDetailsStore) SavePRDetails(_ context.Context, prURL string, providerUpdatedAt time.Time, details domain.PullRequestDetails) error {
	f.saved = append(f.saved, details)
	f.savedURL, f.updatedAt = prURL, providerUpdatedAt
	return nil
}

// A reader that records the refs it was asked for.
type detailsReader struct {
	observation ports.SCMObservation
	refs        []ports.SCMPRRef
}

func (r *detailsReader) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	r.refs = append(r.refs, refs...)
	return []ports.SCMObservation{r.observation}, nil
}

func (r *detailsReader) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return ports.SCMReviewObservation{}, nil
}

var loadedAt = time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)

func detailsFixture(merged bool) (*ActionService, *fakeDetailsStore, *detailsReader, domain.PullRequest) {
	tracked := domain.PullRequest{URL: "https://github.com/acme/widgets/pull/7", Number: 7, Provider: "github", Host: "github.com", Repo: "acme/widgets", Merged: merged}
	updated := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	reader := &detailsReader{observation: ports.SCMObservation{
		Fetched: true,
		PR: ports.SCMPRObservation{
			URL: tracked.URL, Number: 7, Merged: merged, UpdatedAtProvider: updated,
			Details: &domain.PullRequestDetails{Description: "Split panes", RequestedReviewers: []domain.PullRequestRequestedReviewer{{Login: "ada"}}},
		},
	}}
	store := &fakeDetailsStore{}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: tracked, ok: true}, Reader: reader, Details: store, Clock: func() time.Time { return loadedAt }})
	return svc, store, reader, tracked
}

// The observer follows open PRs only, so a PR merged before its details were
// read gets them here, once, without its row changing.
func TestLoadDetails_ReadsAMergedPRsDetailsOnce(t *testing.T) {
	svc, store, reader, tracked := detailsFixture(true)
	if err := svc.LoadDetails(context.Background(), "7", tracked.URL); err != nil {
		t.Fatal(err)
	}
	if len(reader.refs) != 1 || !reader.refs[0].DetailsUpdatedAt.IsZero() {
		t.Fatalf("refs = %#v, want one ref asking for details", reader.refs)
	}
	if len(store.saved) != 1 || store.saved[0].Description != "Split panes" || store.savedURL != tracked.URL || !store.updatedAt.Equal(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("saved = %#v url = %q updatedAt = %v", store.saved, store.savedURL, store.updatedAt)
	}
	// Views treat details with no time as not read yet.
	if !store.saved[0].ObservedAt.Equal(loadedAt) {
		t.Fatalf("details observedAt = %v, want %v", store.saved[0].ObservedAt, loadedAt)
	}
}

func TestLoadDetails_LeavesStoredDetailsToTheObserver(t *testing.T) {
	svc, store, reader, tracked := detailsFixture(false)
	store.stored = true
	if err := svc.LoadDetails(context.Background(), "7", tracked.URL); err != nil {
		t.Fatal(err)
	}
	if len(reader.refs) != 0 || len(store.saved) != 0 {
		t.Fatalf("refs = %d saved = %d, want neither", len(reader.refs), len(store.saved))
	}
}

func TestLoadDetails_FailsWhenTheProviderSendsNone(t *testing.T) {
	svc, store, reader, tracked := detailsFixture(true)
	reader.observation.PR.Details = nil
	if err := svc.LoadDetails(context.Background(), "7", tracked.URL); err == nil {
		t.Fatal("want an error when the provider sends no details")
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved = %#v", store.saved)
	}
}

func TestLoadDetails_RefusesAnUntrackedPR(t *testing.T) {
	svc, _, _, _ := detailsFixture(true)
	if err := svc.LoadDetails(context.Background(), "8", "https://github.com/acme/widgets/pull/7"); err == nil {
		t.Fatal("want an error for a number that is not the tracked PR's")
	}
}

// A miss keeps the provider's reason: only a PR that is gone is "not found".
func TestLoadDetails_KeepsTheProvidersReasonForAMiss(t *testing.T) {
	limited := errors.New("github: rate limited")
	for _, tc := range []struct {
		name     string
		err      error
		notFound bool
	}{
		{"transient", limited, false},
		{"gone", ports.ErrSCMNotFound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, reader, tracked := detailsFixture(true)
			reader.observation = ports.SCMObservation{Error: tc.err}
			err := svc.LoadDetails(context.Background(), "7", tracked.URL)
			if !errors.Is(err, tc.err) || errors.Is(err, ErrPRNotFound) != tc.notFound {
				t.Fatalf("err = %v, want %v with not-found = %v", err, tc.err, tc.notFound)
			}
			if len(store.saved) != 0 {
				t.Fatalf("saved = %#v", store.saved)
			}
		})
	}
}
