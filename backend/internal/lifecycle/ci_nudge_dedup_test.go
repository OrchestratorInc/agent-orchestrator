package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A still-failing check that is merely re-run (new check-run URL, same name,
// commit and status) must not re-nudge the worker. Before the signature fix,
// ciFailureSignature included the per-attempt URL, so the dedup in sendOnce
// missed and the worker was nudged again for a failure it already saw.
func TestPRObservation_CIReRunDoesNotReNudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	obs := func(checkRunURL string) ports.PRObservation {
		return ports.PRObservation{
			Fetched: true,
			URL:     "pr1", // the PR (dedup key) is constant
			CI:      domain.CIFailing,
			Checks: []ports.PRCheckObservation{{
				Name:       "build",
				CommitHash: "c1",
				Status:     domain.PRCheckFailed,
				URL:        checkRunURL, // the check-run URL changes per re-run attempt
			}},
		}
	}

	if err := m.ApplyPRObservation(ctx, "mer-1", obs("https://ci/runs/1/job/100")); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("first failing observation should nudge once, got %d: %v", len(msg.msgs), msg.msgs)
	}

	// Re-run: identical failure, new check-run URL only. Must not re-nudge.
	if err := m.ApplyPRObservation(ctx, "mer-1", obs("https://ci/runs/1/job/200")); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("re-run of the same failing check must not re-nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// A duplicate same-named check (the same job reported under both push and
// pull_request events) and a reordered check set must not change the signature
// or re-nudge; a genuinely new failing check must.
func TestPRObservation_CISignatureStableAcrossOrderAndDuplicates(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	build := ports.PRCheckObservation{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, URL: "https://ci/a"}
	buildDupDifferentURL := ports.PRCheckObservation{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, URL: "https://ci/b"}
	test := ports.PRCheckObservation{Name: "test", CommitHash: "c1", Status: domain.PRCheckFailed, URL: "https://ci/c"}

	apply := func(checks ...ports.PRCheckObservation) {
		t.Helper()
		if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
			Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: checks,
		}); err != nil {
			t.Fatal(err)
		}
	}

	apply(build, test)       // first observation -> nudge once
	apply(test, build)       // reordered, same set -> no re-nudge
	apply(build, buildDupDifferentURL, test) // duplicate same-named run -> no re-nudge
	if len(msg.msgs) != 1 {
		t.Fatalf("order and duplicate same-named runs must not re-nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}

	// A genuinely new failing check is new information and must nudge.
	apply(build, test, ports.PRCheckObservation{Name: "lint", CommitHash: "c1", Status: domain.PRCheckFailed, URL: "https://ci/d"})
	if len(msg.msgs) != 2 {
		t.Fatalf("a newly failing check should nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}
