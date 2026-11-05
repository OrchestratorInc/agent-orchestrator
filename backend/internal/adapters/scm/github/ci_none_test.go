package github

import (
	"testing"

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

// Break caught (#7044): GitHub returns a null statusCheckRollup for a commit
// with no check runs and no statuses (a repository with no workflows, or with
// Actions disabled). That was reported as CIUnknown, which the PR panel reads as
// "checks still loading", so it waited forever.
func TestNullRollupIsNoChecksNotUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		pr   map[string]any
		want domain.CIState
	}{
		{"null rollup on a head commit", prWithRollup(nil, "abc123"), domain.CINone},
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
	obs := scmObservationFromGraphQL(ports.SCMPRRef{Number: 1, URL: "https://github.com/o/r/pull/1"}, prWithRollup(nil, "abc123"))
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
	obs := scmObservationFromGraphQL(ports.SCMPRRef{Number: 1, URL: "https://github.com/o/r/pull/1"}, pr)
	if obs.CI.Summary != string(domain.CINone) {
		t.Errorf("CI.Summary = %q, want %q", obs.CI.Summary, domain.CINone)
	}
	if obs.Mergeability.State != string(domain.MergeBlocked) {
		t.Errorf("Mergeability = %q, want blocked", obs.Mergeability.State)
	}
}
