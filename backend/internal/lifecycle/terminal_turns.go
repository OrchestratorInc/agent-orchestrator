package lifecycle

import (
	"cmp"
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type terminalTurnRecorder interface {
	RecordTerminalTurnRequest(context.Context, domain.TerminalTurn) error
	RecordTerminalTurnResponse(context.Context, domain.SessionID, string, time.Time) error
}

// recordTerminalTurn keeps AO's own record of a Terminal UI turn: the request
// from the prompt hook and the agent's final message from the stop hook. Chat
// turns live in the conversation tables, so the caller records only in terminal
// mode. A failure here must never reject the activity signal itself.
func (m *Manager) recordTerminalTurn(ctx context.Context, rec domain.SessionRecord, s ports.ActivitySignal, now time.Time) {
	recorder, ok := m.store.(terminalTurnRecorder)
	if !ok {
		return
	}
	at := timeOr(s.Timestamp, now)
	var err error
	switch s.Event {
	case "user-prompt-submit":
		request := strings.TrimSpace(s.TurnUserRequest)
		if s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination || request == "" {
			// The user did not ask for this turn: AO's own message opened it,
			// or the hook carried no text. Close any turn still open so the
			// coming stop cannot answer an earlier request.
			err = recorder.RecordTerminalTurnResponse(ctx, rec.ID, "", at)
			break
		}
		err = recorder.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: rec.ID, Harness: rec.Harness, UserRequest: request, RequestedAt: at,
			NativeSessionID: cmp.Or(s.AgentSessionID, rec.Metadata.AgentSessionID),
		})
	case "stop":
		// The turn is over even when the hook carries no text. Closing it
		// keeps the next request from piling onto this one.
		if s.SubagentID == "" && !staleTerminalStop(rec, s) {
			err = recorder.RecordTerminalTurnResponse(ctx, rec.ID, strings.TrimSpace(s.FinalAssistantMessage), at)
		}
	}
	if err != nil {
		slog.Default().Warn("lifecycle: record terminal turn", "session", rec.ID, "event", s.Event, "error", err)
	}
}

// staleTerminalStop reports a Stop that belongs to an earlier turn than the
// one the open terminal row holds: it is older than the latest prompt, or it
// names a different turn than the one that prompt opened. Recording it would
// close the newer request with an older answer, so it is left for the
// lifecycle checkpoint to reject.
func staleTerminalStop(rec domain.SessionRecord, s ports.ActivitySignal) bool {
	md := rec.Metadata
	if !s.Timestamp.IsZero() && !md.LatestUserPromptAt.IsZero() && s.Timestamp.Before(md.LatestUserPromptAt) {
		return true
	}
	return s.ProviderTurnID != "" && md.ConversationCheckpointState == domain.ConversationCheckpointPrompt &&
		md.ConversationCheckpointTurnID != "" && md.ConversationCheckpointTurnID != s.ProviderTurnID
}
