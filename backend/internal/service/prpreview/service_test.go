package prpreview

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeReader struct {
	calls atomic.Int32
	obs   ports.SCMObservation
	err   error
}

func (f *fakeReader) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	if len(refs) != 1 || refs[0].Repo.Repo != "acme/api" || refs[0].Number != 12 {
		return nil, errors.New("unexpected ref")
	}
	return []ports.SCMObservation{f.obs}, nil
}

func TestParseURL(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/acme/api/pull/12",
		"https://github.com/acme/api/pull/12/files",
		"https://www.github.com/acme/api/pull/12#discussion_r1",
	} {
		repo, n, err := ParseURL(raw)
		if err != nil || repo.Repo != "acme/api" || repo.Provider != "github" || n != 12 {
			t.Fatalf("ParseURL(%q) = %+v, %d, %v", raw, repo, n, err)
		}
	}
	for _, raw := range []string{
		"",
		"https://github.com/acme/api/issues/12",
		"https://github.com/acme/api/pull/x",
		"https://gitlab.com/acme/api/pull/12",
		"file:///github.com/acme/api/pull/12",
	} {
		if _, _, err := ParseURL(raw); err == nil {
			t.Fatalf("ParseURL(%q) accepted", raw)
		}
	}
}

func TestPreviewMapsAndCaches(t *testing.T) {
	merged := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	reader := &fakeReader{obs: ports.SCMObservation{
		Fetched: true,
		Repo:    "acme/api",
		PR: ports.SCMPRObservation{
			URL: "https://github.com/acme/api/pull/12", Number: 12, Title: "Fix it",
			Merged: true, SourceBranch: "fix", TargetBranch: "main",
			Additions: 42, Deletions: 8, ChangedFiles: 3, Author: "mira",
			MergedAtProvider: merged,
		},
		CI: ports.SCMCIObservation{Summary: "passing"},
	}}
	svc := New(reader)
	for range 2 {
		p, err := svc.Preview(context.Background(), "https://github.com/acme/api/pull/12/files")
		if err != nil {
			t.Fatal(err)
		}
		if p.State != "merged" || p.Checks != "passing" || p.Title != "Fix it" || !p.StateChangedAt.Equal(merged) || p.Additions != 42 {
			t.Fatalf("preview = %+v", p)
		}
	}
	if got := reader.calls.Load(); got != 1 {
		t.Fatalf("reader called %d times, want 1 (cached)", got)
	}
}

func TestPreviewStates(t *testing.T) {
	cases := map[string]ports.SCMPRObservation{
		"open":   {},
		"draft":  {Draft: true},
		"closed": {Closed: true},
		"merged": {Merged: true, Closed: true},
	}
	for want, pr := range cases {
		reader := &fakeReader{obs: ports.SCMObservation{Fetched: true, PR: pr}}
		p, err := New(reader).Preview(context.Background(), "https://github.com/acme/api/pull/12")
		if err != nil || p.State != want {
			t.Fatalf("state = %q, %v; want %q", p.State, err, want)
		}
		if p.Checks != "" {
			t.Fatalf("checks = %q for a PR with no CI summary", p.Checks)
		}
	}
}

func TestPreviewFailures(t *testing.T) {
	notFound := &fakeReader{obs: ports.SCMObservation{Error: ports.ErrSCMNotFound}}
	_, err := New(notFound).Preview(context.Background(), "https://github.com/acme/api/pull/12")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindNotFound {
		t.Fatalf("not found err = %v", err)
	}

	failing := &fakeReader{err: errors.New("rate limited")}
	svc := New(failing)
	if _, err := svc.Preview(context.Background(), "https://github.com/acme/api/pull/12"); !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("fetch err = %v", err)
	}
	// A failure is cached briefly, then asked again.
	now := time.Now()
	svc.now = func() time.Time { return now.Add(fetchFailureTTL) }
	_, _ = svc.Preview(context.Background(), "https://github.com/acme/api/pull/12")
	if got := failing.calls.Load(); got != 2 {
		t.Fatalf("reader called %d times after the failure TTL, want 2", got)
	}
}
