package github

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func prWithRollup(roll any, headRefOid string) map[string]any {
	commit := map[string]any{"oid": headRefOid, "statusCheckRollup": roll}
	if headRefOid == "" {
		return map[string]any{"commits": map[string]any{"nodes": []any{}}}
	}
	return map[string]any{
		"id": "PR_1", "number": float64(1), "state": "OPEN", "mergeable": "MERGEABLE",
		"mergeStateStatus": "CLEAN", "headRefOid": headRefOid, "url": "https://github.com/o/r/pull/1",
		"commits": map[string]any{"nodes": []any{map[string]any{"commit": commit}}},
	}
}

// A null rollup needs provider evidence before it means no checks (#7044).
func TestNullRollupNeedsNoChecksEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		pr   map[string]any
		want domain.CIState
	}{
		{"null rollup on a head commit", prWithRollup(nil, "abc123"), domain.CIUnknown},
		{"no head commit at all", prWithRollup(nil, ""), domain.CIUnknown},
		{"rollup SUCCESS with no nodes", prWithRollup(map[string]any{"state": "SUCCESS", "contexts": map[string]any{"nodes": []any{}}}, "abc123"), domain.CIPassing},
		{"rollup PENDING with no nodes", prWithRollup(map[string]any{"state": "PENDING", "contexts": map[string]any{"nodes": []any{}}}, "abc123"), domain.CIPending},
		{"rollup FAILURE with no nodes", prWithRollup(map[string]any{"state": "FAILURE", "contexts": map[string]any{"nodes": []any{}}}, "abc123"), domain.CIFailing},
		{"rollup with an unrecognized state stays unknown", prWithRollup(map[string]any{"state": "", "contexts": map[string]any{"nodes": []any{}}}, "abc123"), domain.CIUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ciSummaryFromRollupState(tc.pr); got != tc.want {
				t.Errorf("ciSummaryFromRollupState = %q, want %q", got, tc.want)
			}
			if got := ciSummaryFromGraphQL(tc.pr); got != tc.want {
				t.Errorf("ciSummaryFromGraphQL = %q, want %q", got, tc.want)
			}
		})
	}
}

// The observation the observer persists must carry the state, and GitHub's own
// merge state must still decide mergeability for a PR with no checks.
func TestObservationForPRWithoutChecks(t *testing.T) {
	pr := prWithRollup(nil, "abc123")
	pr["noRollupCI"] = domain.CINone
	obs := scmObservationFromGraphQL(ports.SCMPRRef{Number: 1, URL: "https://github.com/o/r/pull/1"}, pr)
	if obs.CI.Summary != string(domain.CINone) {
		t.Errorf("CI.Summary = %q, want %q", obs.CI.Summary, domain.CINone)
	}
	if len(obs.CI.Checks) != 0 || len(obs.CI.FailedChecks) != 0 {
		t.Errorf("checks = %d, failed = %d, want none", len(obs.CI.Checks), len(obs.CI.FailedChecks))
	}
	if obs.Mergeability.State != string(domain.MergeMergeable) {
		t.Errorf("Mergeability = %q, want mergeable", obs.Mergeability.State)
	}
}

// A blocked PR with no checks (branch protection requiring a check that never
// runs) must stay blocked: no checks must not read as mergeable.
func TestObservationForBlockedPRWithoutChecks(t *testing.T) {
	pr := prWithRollup(nil, "abc123")
	pr["mergeStateStatus"] = "BLOCKED"
	pr["noRollupCI"] = domain.CINone
	obs := scmObservationFromGraphQL(ports.SCMPRRef{Number: 1, URL: "https://github.com/o/r/pull/1"}, pr)
	if obs.CI.Summary != string(domain.CINone) {
		t.Errorf("CI.Summary = %q, want %q", obs.CI.Summary, domain.CINone)
	}
	if obs.Mergeability.State != string(domain.MergeBlocked) {
		t.Errorf("Mergeability = %q, want blocked", obs.Mergeability.State)
	}
}

// Captured on 2026-11-14: CLEAN/MERGEABLE, null rollup, Actions run
// 43576558288 pending behind workflow concurrency, before any jobs exist.
func TestNullRollupWorkflowEvidence(t *testing.T) {
	const sha = "1b24d8e808eaf24cb2b488420ea1021a037fd1e5"
	for _, tc := range []struct {
		name, runs string
		age        time.Duration
		want       domain.CIState
		unreadable bool
	}{
		{"concurrency pending", `{"total_count":1,"workflow_runs":[{"id":43576558288,"head_sha":"` + sha + `","status":"pending","conclusion":null}]}`, time.Hour, domain.CIPending, false},
		{"no workflows settled", `{"total_count":0,"workflow_runs":[]}`, time.Hour, domain.CINone, false},
		{"old commit just pushed", `{"total_count":0,"workflow_runs":[]}`, time.Hour, domain.CIUnknown, false},
		{"future commit settled", `{"total_count":0,"workflow_runs":[]}`, -time.Hour, domain.CINone, false},
		{"just pushed", `{"total_count":0,"workflow_runs":[]}`, time.Second, domain.CIUnknown, false},
		{"unread workflow page", `{"total_count":101,"workflow_runs":[]}`, time.Hour, domain.CIPending, false},
		{"unreadable workflows", ``, time.Hour, domain.CIUnknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			pr := prWithRollup(nil, sha)
			f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": pr}, "pr0": map[string]any{"pullRequest": pr}, "pr1": map[string]any{"pullRequest": prWithRollup(map[string]any{"state": "SUCCESS"}, sha)}}})
			})
			f.on(http.MethodGet, "/repos/o/r/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"state":"open","head":{"sha":"` + sha + `"}}`))
			})
			f.on(http.MethodGet, "/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("head_sha") != sha {
					t.Error("workflow query must match the PR head")
				}
				if tc.unreadable {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_, _ = w.Write([]byte(tc.runs))
			})
			f.on(http.MethodGet, "/repos/o/r/commits/"+sha+"/check-suites", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"total_count":0,"check_suites":[]}`))
			})
			f.on(http.MethodGet, "/repos/o/r/commits/"+sha+"/status", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"total_count":0}`)) })
			f.on(http.MethodGet, "/repos/o/r/commits/"+sha, func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"committer": map[string]any{"date": time.Now().Add(-tc.age)}}})
			})
			p := newProviderForTest(t, f)
			if tc.want == domain.CINone {
				now := time.Now()
				p.now = func() time.Time { return now }
				if _, err := p.HeadChecks(ctx(), ports.SCMRepo{Owner: "o", Name: "r"}, sha); err != nil {
					t.Fatal(err)
				}
				now = now.Add(2 * time.Minute)
			}
			legacy, err := p.Observe(ctx(), "https://github.com/o/r/pull/1")
			if tc.unreadable {
				if err == nil || legacy.Fetched {
					t.Fatal("unreadable workflows must not yield a fetched observation")
				}
			} else if err != nil || legacy.CI != tc.want {
				t.Fatalf("Observe CI=%s err=%v, want %s", legacy.CI, err, tc.want)
			}
			refs := []ports.SCMPRRef{{Repo: ports.SCMRepo{Owner: "o", Name: "r"}, Number: 1}}
			if tc.unreadable {
				refs = append(refs, ports.SCMPRRef{Repo: refs[0].Repo, Number: 2})
			}
			obs, err := p.FetchPullRequests(ctx(), refs)
			if tc.unreadable {
				if err != nil || len(obs) != 2 || obs[0].Fetched || obs[0].Error == nil || !obs[1].Fetched || obs[1].CI.Summary != string(domain.CIPassing) {
					t.Fatalf("permission failure must leave only the first PR unresolved: obs=%+v err=%v", obs, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if obs[0].CI.Summary != string(tc.want) {
				t.Fatalf("batch CI=%s, want %s", obs[0].CI.Summary, tc.want)
			}
			ready := domain.MergeReadiness{CI: domain.CIState(obs[0].CI.Summary), Mergeability: domain.Mergeability(obs[0].Mergeability.State), Review: domain.ReviewNone}.ReadyToMerge()
			if ready != (tc.want == domain.CINone) {
				t.Fatalf("ready=%v, CI=%s", ready, tc.want)
			}
		})
	}
}
