package controllers

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Display copies retain source/navigation metadata but omit frozen delivery context.
// Never clear Turn on the original: it can still be in use by delivery or retries.
func excerptSnapshotSummary(captured *domain.ChatExcerptSnapshot) *domain.ChatExcerptSnapshot {
	if captured == nil {
		return nil
	}
	snapshot := *captured
	snapshot.Turn = nil
	return &snapshot
}

func sideTurnSummary(turn domain.SideTurn) domain.SideTurn {
	if turn.References != nil {
		references := make([]domain.SideReference, len(turn.References))
		copy(references, turn.References)
		for i := range references {
			references[i].Snapshot = excerptSnapshotSummary(references[i].Snapshot)
		}
		turn.References = references
	}
	return turn
}

func sideSnapshotSummary(snapshot domain.SideSnapshot) domain.SideSnapshot {
	if snapshot.Turns != nil {
		turns := make([]domain.SideTurn, len(snapshot.Turns))
		for i, turn := range snapshot.Turns {
			turns[i] = sideTurnSummary(turn)
		}
		snapshot.Turns = turns
	}
	return snapshot
}
