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
	got, err := newProviderForTest(t, f).HeadChecks(ctx(), stackRepo, stackHead)
	if err != nil {
		t.Fatal(err)
	}
	if got.Suites != 4 || got.Started != 1 || got.Statuses != 0 || !got.Since.Equal(pushed) {
		t.Fatalf("got = %#v", got)
	}
}

// With no suites at all, the commit's own date is the earliest checks could start.
func TestHeadChecks_UsesTheCommitDateWithoutSuites(t *testing.T) {
	f := newFakeGH(t)
	committed := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead+"/check-suites", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "check_suites": []any{}})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead+"/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "success", "total_count": 1})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/commits/"+stackHead, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"committer": map[string]any{"date": committed}}})
	})
	got, err := newProviderForTest(t, f).HeadChecks(ctx(), stackRepo, stackHead)
	if err != nil {
		t.Fatal(err)
	}
	if got.Suites != 0 || got.Started != 0 || got.Statuses != 1 || !got.Since.Equal(committed) {
		t.Fatalf("got = %#v", got)
	}
}
