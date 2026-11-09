package chat

import (
	"context"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// findSnapshot takes one lock and never calls SideSnapshot, which can restore a
// provider. Side transcripts stay in the existing launch-owned memory store.
func (m *memorySideStore) findSnapshot(ctx context.Context, id string) (domain.SideSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.SideSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[id]
	if !ok || side.ClosedAt != nil || side.AppRunID != m.runID {
		return domain.SideSnapshot{}, ErrFindUnavailable
	}
	result := domain.SideSnapshot{Side: side}
	visible := map[string]bool{}
	for _, turn := range m.turns[id] {
		if turn.State != "queued" && turn.State != "cancelled" {
			visible[turn.ID] = true
			result.Turns = append(result.Turns, turn)
		}
	}
	for _, message := range m.messages[id] {
		if visible[message.TurnID] {
			result.Messages = append(result.Messages, message)
		}
	}
	for _, activity := range m.activities[id] {
		if visible[activity.TurnID] {
			result.Activities = append(result.Activities, activity)
		}
	}
	sort.SliceStable(result.Messages, func(i, j int) bool { return result.Messages[i].Sequence < result.Messages[j].Sequence })
	return result, nil
}

func (s *Service) findSide(ctx context.Context, id string) (Snapshot, error) {
	if s.sides == nil {
		return Snapshot{}, ErrFindUnavailable
	}
	reader, ok := s.sides.store.(interface {
		findSnapshot(context.Context, string) (domain.SideSnapshot, error)
	})
	if !ok {
		return Snapshot{}, ErrFindUnavailable
	}
	side, err := reader.findSnapshot(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Conversation: domain.ConversationRecord{ID: id, SessionID: side.Side.SessionID}, SessionID: side.Side.SessionID, Mode: domain.SessionModeChat}
	for _, message := range side.Messages {
		origin := domain.MessageOriginProvider
		if message.Role == "user" {
			origin = domain.MessageOriginHuman
		}
		result.Messages = append(result.Messages, domain.ConversationMessage{ID: message.ID, ConversationID: id, TurnID: message.TurnID, Role: domain.MessageRole(message.Role), Origin: origin, Text: message.Text, Sequence: message.Sequence, Revision: message.Revision, Streaming: message.Streaming, CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt})
		result.Conversation.LatestSequence = max(result.Conversation.LatestSequence, message.Sequence)
	}
	for _, turn := range side.Turns {
		result.Turns = append(result.Turns, domain.ConversationTurn{ID: turn.ID, State: domain.TurnState(turn.State), RequestedAt: turn.CreatedAt, StartedAt: turn.StartedAt, CompletedAt: turn.CompletedAt})
	}
	for _, activity := range side.Activities {
		result.Activities = append(result.Activities, domain.ConversationActivity{ID: activity.ID, ConversationID: id, TurnID: activity.TurnID, Kind: domain.ActivityKind(activity.Kind), Status: domain.ActivityStatus(activity.Status), Summary: activity.Summary, Detail: activity.Detail, CreatedAt: activity.CreatedAt})
	}
	return result, nil
}
