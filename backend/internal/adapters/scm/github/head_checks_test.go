package github

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestHeadChecks_ReadsSuitesStatusesAndWhenChecksCouldStart(t *testing.T) {
	f := newFakeGH(t)
	pushed := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead+"/check-suites", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 4, "check_suites": []map[string]any{
			// Apps installed on the repository that never run here.
			{"status": "queued", "conclusion": nil, "latest_check_runs_count": 0, "created_at": pushed.Add(time.Second)},
			{"status": "queued", "conclusion": nil, "latest_check_runs_count": 0, "created_at": pushed},
			// A workflow that matched nothing.
			{"status": "completed", "conclusion": "skipped", "latest_check_runs_count": 0, "created_at": pushed.Add(2 * time.Second)},
			// A run waiting for a maintainer's approval checks nothing yet, but will.
			{"status": "completed", "conclusion": "action_required", "latest_check_runs_count": 0, "created_at": pushed.Add(3 * time.Second)},
		}})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead+"/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "pending", "total_count": 0, "statuses": []any{}})
	})
	p := newProviderForTest(t, f)
	p.now = func() time.Time { return pushed }
	got, err := p.HeadChecks(ctx(), stackRepo, stackHead)
	if err != nil {
		t.Fatal(err)
	}
	if got.Suites != 4 || got.Started != 1 || got.Statuses != 0 || !got.Since.Equal(pushed) {
		t.Fatalf("got = %#v", got)
	}
}

// A future commit date must not extend the observation grace period.
func TestHeadChecks_UsesObservationTimeWithoutSuites(t *testing.T) {
	f := newFakeGH(t)
	observed := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	committed := observed.Add(time.Hour)
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead+"/check-suites", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "check_suites": []any{}})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead+"/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "success", "total_count": 1})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"committer": map[string]any{"date": committed}}})
	})
	p := newProviderForTest(t, f)
	p.now = func() time.Time { return observed }
	got, err := p.HeadChecks(ctx(), stackRepo, stackHead)
	if err != nil {
		t.Fatal(err)
	}
	if got.Suites != 0 || got.Started != 0 || got.Statuses != 1 || !got.Since.Equal(observed) {
		t.Fatalf("got = %#v", got)
	}
}

// Commit and suite dates describe old objects, not when this head was pushed.
func TestHeadChecksGraceStartsAtObservation(t *testing.T) {
	f := newFakeGH(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	p := newProviderForTest(t, f)
	p.now = func() time.Time { return now }
	for _, sha := range []string{stackHead, "new-head"} {
		f.on(http.MethodGet, "/repos/octocat/hello/commits/"+sha+"/check-suites", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"total_count":0,"check_suites":[]}`))
		})
		f.on(http.MethodGet, "/repos/octocat/hello/commits/"+sha+"/status", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"total_count":0}`)) })
		f.on(http.MethodGet, "/repos/octocat/hello/commits/"+sha, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"committer": map[string]any{"date": now.Add(-time.Hour)}}})
		})
	}
	first, err := p.HeadChecks(ctx(), stackRepo, stackHead)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Since.Equal(now) {
		t.Fatalf("first observation since=%v, want %v", first.Since, now)
	}
	now = now.Add(3 * time.Minute)
	same, err := p.HeadChecks(ctx(), stackRepo, stackHead)
	if err != nil {
		t.Fatal(err)
	}
	next, err := p.HeadChecks(ctx(), stackRepo, "new-head")
	if err != nil {
		t.Fatal(err)
	}
	if !same.Since.Equal(first.Since) || !next.Since.Equal(now) {
		t.Fatalf("same=%v next=%v, want %v and %v", same.Since, next.Since, first.Since, now)
	}
}
