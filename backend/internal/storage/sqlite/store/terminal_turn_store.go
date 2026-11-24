package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RecordTerminalTurnRequest stores what the user asked a Terminal UI agent. A
// request that arrives while the session's newest turn is still open on the
// same thread was queued behind it, so it joins that turn instead of starting
// another.
func (s *Store) RecordTerminalTurnRequest(ctx context.Context, turn domain.TerminalTurn) error {
	text := strings.TrimSpace(turn.UserRequest)
	if text == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "record terminal turn request", func(q *gen.Queries) error {
		joined, err := q.AppendTerminalTurnRequest(ctx, gen.AppendTerminalTurnRequestParams{
			UserRequest: text, SessionID: string(turn.SessionID),
			Harness: string(turn.Harness), NativeSessionID: turn.NativeSessionID,
		})
		if err != nil || joined > 0 {
			return err
		}
		if err := q.InsertTerminalTurn(ctx, gen.InsertTerminalTurnParams{
			SessionID: string(turn.SessionID), Harness: string(turn.Harness),
			NativeSessionID: turn.NativeSessionID, UserRequest: text, RequestedAt: turn.RequestedAt,
		}); err != nil {
			return err
		}
		return q.TrimTerminalTurns(ctx, gen.TrimTerminalTurnsParams{
			SessionID: string(turn.SessionID), Keep: domain.TerminalTurnRetention,
		})
	})
}

// RecordTerminalTurnResponse closes the session's newest turn with the agent's
// final message. A stop with no open turn has nothing to answer and is ignored.
func (s *Store) RecordTerminalTurnResponse(ctx context.Context, id domain.SessionID, response string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CloseTerminalTurn(ctx, gen.CloseTerminalTurnParams{
		AgentResponse: strings.TrimSpace(response), RespondedAt: sql.NullTime{Time: at, Valid: true}, SessionID: string(id),
	}); err != nil {
		return fmt.Errorf("record terminal turn response: %w", err)
	}
	return nil
}

// ListTerminalTurns returns a session's stored terminal turns, oldest first.
func (s *Store) ListTerminalTurns(ctx context.Context, id domain.SessionID) ([]domain.HandoffTurn, error) {
	rows, err := s.qr.ListTerminalTurns(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list terminal turns: %w", err)
	}
	turns := make([]domain.HandoffTurn, len(rows))
	for index, row := range rows {
		turns[index] = domain.HandoffTurn{UserRequest: row.UserRequest, AgentResponse: row.AgentResponse, At: row.RequestedAt, Harness: domain.AgentHarness(row.Harness)}
	}
	return turns, nil
}

// DeleteTerminalTurnsForThread removes the turns of one provider thread after
// its history was imported into the conversation tables, so a turn never sits
// in both stores. Rows recorded before the hook reported a thread id match on
// the provider alone. Other providers' turns were not imported and stay.
func (s *Store) DeleteTerminalTurnsForThread(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, nativeSessionID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteTerminalTurnsForThread(ctx, gen.DeleteTerminalTurnsForThreadParams{
		SessionID: string(id), Harness: string(harness), NativeSessionID: nativeSessionID,
	}); err != nil {
		return fmt.Errorf("delete imported terminal turns: %w", err)
	}
	return nil
}
