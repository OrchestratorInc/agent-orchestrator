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
			err = recorder.RecordTerminalTurnResponse(ctx, rec.ID, "", at)
			break
		}
		err = recorder.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: rec.ID, Harness: rec.Harness, UserRequest: request, RequestedAt: at,
			NativeSessionID: cmp.Or(s.AgentSessionID, rec.Metadata.AgentSessionID),
		})
	case "stop":
		if s.SubagentID == "" && !staleTerminalStop(rec, s) {
			err = recorder.RecordTerminalTurnResponse(ctx, rec.ID, strings.TrimSpace(s.FinalAssistantMessage), at)
		}
	}
	if err != nil {
		slog.Default().Warn("lifecycle: record terminal turn", "session", rec.ID, "event", s.Event, "error", err)
	}
}

func staleTerminalStop(rec domain.SessionRecord, s ports.ActivitySignal) bool {
	md := rec.Metadata
	if !s.Timestamp.IsZero() && !md.LatestUserPromptAt.IsZero() && s.Timestamp.Before(md.LatestUserPromptAt) {
		return true
	}
	return s.ProviderTurnID != "" && md.ConversationCheckpointState == domain.ConversationCheckpointPrompt &&
		md.ConversationCheckpointTurnID != "" && md.ConversationCheckpointTurnID != s.ProviderTurnID
}
