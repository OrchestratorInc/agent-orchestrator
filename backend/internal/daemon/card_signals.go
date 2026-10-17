package daemon

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// cardSignalActivity lets terminal (TUI) sessions feed the card-summary
// scheduler. They have no chat controller, so the only evidence is what the
// agent's own hooks report: prompt submits, tool uses, and the final message.
// The wrapped recorder still handles the signal exactly as before.
type cardSignalActivity struct {
	controllers.ActivityRecorder
	observe func(context.Context, domain.SessionID, domain.CardSignal)
}

func (a cardSignalActivity) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	err := a.ActivityRecorder.ApplyActivitySignal(ctx, id, s)
	// Contention is the lifecycle manager asking a hook to be dropped, not the
	// hook being wrong; the turn boundary it carries still happened.
	if (err == nil || errors.Is(err, ports.ErrActivityProjectionContention)) && a.observe != nil {
		for _, signal := range cardSignalsFromHook(s) {
			a.observe(ctx, id, signal)
		}
	}
	return err
}

// cardSignalsFromHook maps one hook event to card evidence. Subagent events
// belong to nested work, not to what the card should say.
func cardSignalsFromHook(s ports.ActivitySignal) []domain.CardSignal {
	if s.SubagentID != "" {
		return nil
	}
	switch s.Event {
	case "user-prompt-submit":
		return []domain.CardSignal{{Kind: domain.CardSignalTurnStarted}}
	case "post-tool-use":
		if s.ToolName == "" {
			return nil
		}
		return []domain.CardSignal{{Kind: domain.CardSignalActivity, Text: "Used " + s.ToolName}}
	case "post-tool-use-failure", "post-tool-use-fail":
		if s.ToolName == "" {
			return nil
		}
		return []domain.CardSignal{{Kind: domain.CardSignalActivity, Text: "Failed: " + s.ToolName}}
	case "stop":
		signals := make([]domain.CardSignal, 0, 2)
		if s.LatestAssistantUpdate != "" {
			signals = append(signals, domain.CardSignal{Kind: domain.CardSignalAssistant, Text: s.LatestAssistantUpdate})
		}
		return append(signals, domain.CardSignal{Kind: domain.CardSignalTurnCompleted})
	}
	return nil
}
