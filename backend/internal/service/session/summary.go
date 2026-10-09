package session

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// deriveSummary builds the Kanban card line. Priority:
//  1. AI-generated summary set by the agent via the ao summary tool.
//  2. PR / lifecycle status from the contract layer as a fallback.
func deriveSummary(rec domain.SessionRecord, prs []domain.PRFacts, displayStatus contract.DisplayStatus) string {
	if generated := strings.TrimPrefix(strings.TrimSpace(rec.Metadata.LatestAssistantUpdate), domain.CardSummaryMetadataPrefix); generated != strings.TrimSpace(rec.Metadata.LatestAssistantUpdate) && generated != "" {
		return generated
	}
	open, failing := 0, false
	for _, pr := range prs {
		if pr.Merged || pr.Closed {
			continue
		}
		open++
		if pr.CI == domain.CIFailing {
			failing = true
		}
	}
	return contract.SummarizeSession(contract.SummaryFacts{
		OpenPRs:       open,
		CIFailing:     failing,
		DisplayStatus: displayStatus,
	})
}
