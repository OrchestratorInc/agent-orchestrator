package chat

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (s *Service) nativeReplayRows(ctx context.Context, active domain.ConversationBranch, rows ConversationRows) (ConversationRows, error) {
	if active.ProviderScopeID == "" {
		return rows, nil
	}
	branches := map[string]bool{active.ID: true}
	turns := make(map[string]bool)
	filtered := ConversationRows{}
	for _, turn := range rows.Turns {
		eligible, known := branches[turn.BranchID]
		if !known {
			branch, err := s.store.ConversationBranch(ctx, active.ConversationID, turn.BranchID)
			if err != nil {
				return ConversationRows{}, fmt.Errorf("read native replay ownership: %w", err)
			}
			eligible = branch.ProviderScopeID == active.ProviderScopeID
			branches[turn.BranchID] = eligible
		}
		if eligible {
			filtered.Turns = append(filtered.Turns, turn)
			turns[turn.ID] = true
		}
	}
	for _, message := range rows.Messages {
		if turns[message.TurnID] {
			filtered.Messages = append(filtered.Messages, message)
		}
	}
	for _, activity := range rows.Activities {
		if turns[activity.TurnID] {
			filtered.Activities = append(filtered.Activities, activity)
		}
	}
	return filtered, nil
}
