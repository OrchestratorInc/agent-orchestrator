package codexappserver

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	failureTurnID string
	failure       error
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
		var fields map[string]json.RawMessage
		if json.Unmarshal(n.Params, &p) != nil || json.Unmarshal(p.RateLimits, &wire) != nil || json.Unmarshal(p.RateLimits, &fields) != nil {
			return
		}
		if wire.LimitID == nil || *wire.LimitID != "codex" {
			u.ambiguous = true
			return
		}
		_, primaryReported := fields["primary"]
		_, secondaryReported := fields["secondary"]
		if u.ambiguous || !primaryReported || !secondaryReported ||
			(wire.SpendControlReached != nil && *wire.SpendControlReached) ||
			(wire.RateLimitReachedType != nil && *wire.RateLimitReachedType != "rate_limit_reached" && *wire.RateLimitReachedType != "") {
			return
		}
		var latest time.Time
		for _, window := range []*capacityWireWindow{wire.Primary, wire.Secondary} {
			if window == nil {
				continue
			}
			normalized := normalizeCapacityWindow(window)
			if normalized == nil {
				return
			}
			if normalized.UsedPercent < 100 {
				continue
			}
			if normalized.ResetsAt == nil || !normalized.ResetsAt.After(now) {
				return
			}
			if normalized.ResetsAt.After(latest) {
				latest = *normalized.ResetsAt
			}
		}
		u.resetAt = latest
	}
}

func (u *usageLimitTracker) decorate(ev ports.ChatEvent, now time.Time) ports.ChatEvent {
	if ev.ProviderConversationID != u.threadID || ev.ProviderTurnID == "" {
		return ev
	}
	if ev.ProviderTurnID == u.turnID && errors.Is(ev.Err, errCodexUsageLimitExceeded) && !u.ambiguous && u.resetAt.After(now) {
		ev.Err = ports.NewChatProviderFailure("Usage limit exceeded; resets "+u.resetAt.UTC().Format(time.RFC3339), ev.Err.Error(), ev.Err)
	}
	if ev.Kind == ports.ChatEventError {
		u.failureTurnID, u.failure = "", nil
		if errors.Is(ev.Err, errCodexUsageLimitExceeded) {
			u.failureTurnID, u.failure = ev.ProviderTurnID, ev.Err
		}
	}
	if ev.Kind == ports.ChatEventTurnCompleted {
		if ev.Err == nil && ev.TurnState == domain.TurnStateFailed && ev.ProviderTurnID == u.failureTurnID {
			ev.Err = u.failure
		}
		if ev.ProviderTurnID == u.turnID {
			*u = usageLimitTracker{threadID: u.threadID}
		} else if ev.ProviderTurnID == u.failureTurnID {
			u.failureTurnID, u.failure = "", nil
		}
	}
	return ev
}
