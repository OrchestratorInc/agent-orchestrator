package lifecycle

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReviewOnlyPRDoesNotCountAsSessionOutput(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1"}
	st.prs["mer-1"] = []domain.PullRequest{{URL: "https://github.com/o/r/pull/1", ReviewOnly: true}}
	if err := m.ReconcileSessionOutputType(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputNone {
		t.Fatalf("output = %q, want none for a PR the session only reviews", got)
	}
}
