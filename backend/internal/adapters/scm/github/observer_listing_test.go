package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestListPRsByRepoFiltersUpdatesAcrossPages(t *testing.T) {
	cutoff := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	for _, incremental := range []bool{false, true} {
		t.Run(strconv.FormatBool(incremental), func(t *testing.T) {
			f := newFakeGH(t)
			pages := 0
			f.on("GET", "/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
				pages++
				if r.URL.Query().Has("since") {
					t.Error("unsupported since parameter")
				}
				if r.URL.Query().Get("sort") != "updated" || r.URL.Query().Get("direction") != "desc" {
					t.Error("listing is not ordered by update time")
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				var pulls []map[string]any
				if page == 1 {
					for i := 1; i <= 100; i++ {
						pulls = append(pulls, map[string]any{"number": i, "updated_at": cutoff.Add(time.Second).Format(time.RFC3339)})
					}
				} else {
					pulls = []map[string]any{{"number": 101, "updated_at": cutoff.Format(time.RFC3339)}, {"number": 102, "updated_at": cutoff.Add(-time.Second).Format(time.RFC3339)}}
				}
				if err := json.NewEncoder(w).Encode(pulls); err != nil {
					t.Error(err)
				}
			})
			after := time.Time{}
			want := 102
			if incremental {
				after, want = cutoff, 101
			}
			got, err := newProviderForTest(t, f).ListPRsByRepo(context.Background(), ports.SCMRepo{Owner: "o", Name: "r"}, after)
			if err != nil || len(got) != want || pages != 2 {
				t.Fatalf("PRs=%d pages=%d err=%v, want %d PRs", len(got), pages, err, want)
			}
		})
	}
}
