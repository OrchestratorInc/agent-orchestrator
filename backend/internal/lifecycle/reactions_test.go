package lifecycle

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestApplyReviewBatchApprovedOnlyHeader(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	results := []ReviewResult{{
		RunID: "run-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1",
		Verdict: domain.VerdictApproved, Body: "review approved: pr1, head sha1 up to date",
	}}
	if _, err := m.ApplyReviewBatch(ctx, "mer-1", "batch-1", results); err != nil {
		t.Fatalf("ApplyReviewBatch: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %d, want 1", len(msg.msgs))
	}
	if !strings.Contains(msg.msgs[0], "approved 1 review(s)") || strings.Contains(msg.msgs[0], "requesting changes") {
		t.Fatalf("approved-only header wrong: %q", msg.msgs[0])
	}
}

func TestApplyReviewBatchMixedHeaderCountsOnlyChangesRequested(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	results := []ReviewResult{
		{
			RunID: "run-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1",
			Verdict: domain.VerdictApproved, Body: "review approved: pr1, head sha1 up to date",
		},
		{
			RunID: "run-2", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2",
			Verdict: domain.VerdictChangesRequested, Body: "fix it",
		},
	}
	if _, err := m.ApplyReviewBatch(ctx, "mer-1", "batch-1", results); err != nil {
		t.Fatalf("ApplyReviewBatch: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %d, want 1", len(msg.msgs))
	}
	if !strings.Contains(msg.msgs[0], "submitted 1 review(s) requesting changes") {
		t.Fatalf("mixed-batch header counts approved runs too: %q", msg.msgs[0])
	}
	if !strings.Contains(msg.msgs[0], "Verdict: approved") || !strings.Contains(msg.msgs[0], "review approved: pr1, head sha1 up to date") {
		t.Fatalf("approved section lost from mixed batch: %q", msg.msgs[0])
	}
}
