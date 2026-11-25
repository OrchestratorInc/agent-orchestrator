package codexappserver

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

var errCodexUsageLimitExceeded = errors.New("codex usage limit exceeded")

// Account notifications have no turn or thread identifier. Keep only complete
// Codex observations received during this root turn, never a previous turn's
// account cache. A second bucket makes the failed limit ambiguous for this turn.
type usageLimitTracker struct {
	threadID  string
	turnID    string
	resetAt   time.Time
	ambiguous bool
	// Codex can report a terminal error separately, then complete without an
	// attached error. Preserve that quota refusal on the durable failed turn.
	failureTurnID   string
	failure         error
	failureResetsAt time.Time
	// quotaRefused is set while the root thread's latest error is a usage-limit
	// refusal, systemErrorSent while a systemError status went out before that
	// refusal was known, and systemErrorHidden while AO reports idle for a thread
	// Codex still holds in systemError. All last until the next turn starts,
	// because that is when Codex clears the thread's error.
	quotaRefused      bool
	systemErrorSent   bool
	systemErrorHidden bool
}

func (u *usageLimitTracker) observe(n notification, rootThread string, now time.Time) {
	u.threadID = rootThread
	switch n.Method {
	case codexproto.MethodTurnStarted:
		var p codexproto.TurnStartedNotification
		if json.Unmarshal(n.Params, &p) == nil && p.ThreadID == rootThread {
			turnID := p.Turn.ID
			if turnID == "" {
				turnID = turnIDFallback(n.Params)
			}
			*u = usageLimitTracker{threadID: rootThread, turnID: turnID}
		}
	case codexproto.MethodAccountRateLimitsUpdated:
		if u.turnID == "" {
			return
		}
		u.resetAt = time.Time{}
		var p struct {
			RateLimits json.RawMessage `json:"rateLimits"`
		}
		var wire capacityWireBucket
		if json.Unmarshal(n.Params, &p) != nil || json.Unmarshal(p.RateLimits, &wire) != nil {
			return
		}
		if wire.LimitID == nil || *wire.LimitID != "codex" {
			u.ambiguous = true
			return
		}
		if u.ambiguous {
			return
		}
		u.resetAt = codexUsageLimitResetAt(wire, now)
	}
}

// codexUsageLimitResetAt is shared by turn notifications and account reads.
// Display-normalized capacity alone cannot distinguish rolling and spend caps.
func codexUsageLimitResetAt(wire capacityWireBucket, now time.Time) time.Time {
	if wire.LimitID == nil || *wire.LimitID != "codex" || !wire.primaryReported || !wire.secondaryReported ||
		(wire.SpendControlReached != nil && *wire.SpendControlReached) ||
		(wire.RateLimitReachedType != nil && *wire.RateLimitReachedType != "rate_limit_reached" && *wire.RateLimitReachedType != "") {
		return time.Time{}
	}
	var latest time.Time
	for _, window := range []*capacityWireWindow{wire.Primary, wire.Secondary} {
		if window == nil {
			continue
		}
		normalized := normalizeCapacityWindow(window)
		if normalized == nil {
			return time.Time{}
		}
		if normalized.UsedPercent < 100 {
			continue
		}
		if normalized.ResetsAt == nil || !normalized.ResetsAt.After(now) {
			return time.Time{}
		}
		if normalized.ResetsAt.After(latest) {
			latest = *normalized.ResetsAt
		}
	}
	return latest
}

func (u *usageLimitTracker) decorate(ev ports.ChatEvent, now time.Time) ports.ChatEvent {
	if ev.ProviderConversationID != u.threadID || ev.ProviderTurnID == "" {
		return ev
	}
	if ev.ProviderTurnID == u.turnID && errors.Is(ev.Err, errCodexUsageLimitExceeded) && !u.ambiguous && u.resetAt.After(now) {
		ev.UsageLimitResetsAt = u.resetAt
	}
	if ev.Kind == ports.ChatEventError {
		u.failureTurnID, u.failure, u.failureResetsAt = "", nil, time.Time{}
		if errors.Is(ev.Err, errCodexUsageLimitExceeded) {
			u.failureTurnID, u.failure, u.failureResetsAt = ev.ProviderTurnID, ev.Err, ev.UsageLimitResetsAt
		}
	}
	if ev.Kind == ports.ChatEventTurnCompleted {
		if ev.Err == nil && ev.TurnState == domain.TurnStateFailed && ev.ProviderTurnID == u.failureTurnID {
			ev.Err, ev.UsageLimitResetsAt = u.failure, u.failureResetsAt
		}
		switch ev.ProviderTurnID {
		case u.turnID:
			*u = usageLimitTracker{threadID: u.threadID, quotaRefused: u.quotaRefused,
				systemErrorSent: u.systemErrorSent, systemErrorHidden: u.systemErrorHidden}
		case u.failureTurnID:
			u.failureTurnID, u.failure, u.failureResetsAt = "", nil, time.Time{}
		}
	}
	return ev
}

// threadStatus keeps a usage-limit refusal from reading as a broken thread.
//
// Codex marks a thread systemError on every error, a quota refusal included,
// until its next turn starts. The thread is fine and the refusal already has its
// own failure card, so that status is reported as idle. Codex sends the status
// and the error independently, so when the systemError went out first, the
// refusal is followed by an idle that corrects it. Codex reports a status only
// when it changes, so a later error that is not a refusal restores the hidden
// systemError itself.
func (u *usageLimitTracker) threadStatus(ev ports.ChatEvent, thread string) (ports.ChatEvent, *ports.ChatEvent) {
	if thread != u.threadID {
		return ev, nil
	}
	switch ev.Kind {
	case ports.ChatEventThreadState:
		if ev.ThreadState == nil || ev.ThreadState.Status == "" {
			return ev, nil
		}
		u.systemErrorSent, u.systemErrorHidden = false, false
		if ev.ThreadState.Status != domain.ThreadStatusSystemError {
			return ev, nil
		}
		if !u.quotaRefused {
			u.systemErrorSent = true
			return ev, nil
		}
		state := *ev.ThreadState
		state.Status, state.WaitingOn = domain.ThreadStatusIdle, nil
		ev.ThreadState = &state
		u.systemErrorHidden = true
	case ports.ChatEventError, ports.ChatEventTurnCompleted:
		if ev.Err == nil {
			return ev, nil
		}
		u.quotaRefused = errors.Is(ev.Err, errCodexUsageLimitExceeded)
		switch {
		case u.quotaRefused && u.systemErrorSent:
			u.systemErrorSent, u.systemErrorHidden = false, true
			return ev, &ports.ChatEvent{
				Kind:        ports.ChatEventThreadState,
				ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusIdle},
			}
		case !u.quotaRefused && u.systemErrorHidden:
			u.systemErrorSent, u.systemErrorHidden = true, false
			return ev, &ports.ChatEvent{
				Kind:        ports.ChatEventThreadState,
				ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusSystemError},
			}
		}
	}
	return ev, nil
}
