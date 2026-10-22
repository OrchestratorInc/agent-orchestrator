package contract_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestSummarizeSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		facts contract.SummaryFacts
		want  string
	}{
		{
			name:  "working status",
			facts: contract.SummaryFacts{DisplayStatus: contract.DisplayWorking},
			want:  "Working",
		},
		{
			name:  "awaiting PR is working",
			facts: contract.SummaryFacts{DisplayStatus: contract.DisplayAwaitingPR},
			want:  "Working",
		},
		{
			name:  "blocked is stalled",
			facts: contract.SummaryFacts{DisplayStatus: contract.DisplayBlocked},
			want:  "Stalled",
		},
		{
			name:  "terminated is stalled",
			facts: contract.SummaryFacts{DisplayStatus: contract.DisplayTerminated},
			want:  "Stalled",
		},
		{
			name:  "ci failure takes priority over status",
			facts: contract.SummaryFacts{CIFailing: true, OpenPRs: 1},
			want:  "CI failing on open PR",
		},
		{
			name:  "open pr",
			facts: contract.SummaryFacts{OpenPRs: 2},
			want:  "PR open",
		},
		{
			name:  "empty facts yield empty summary",
			facts: contract.SummaryFacts{},
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := contract.SummarizeSession(tc.facts); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}
